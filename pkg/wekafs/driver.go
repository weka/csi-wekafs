package wekafs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"os"
	"os/signal"
	"syscall"
)

type WekaFsDriver struct {
	name              string
	nodeID            string
	version           string
	endpoint          string
	maxVolumesPerNode int64
	mountMode         string
	mockMount         bool

	ids *identityServer
	ns  *NodeServer
	cs  *ControllerServer
	ms  *MetricsServer
	api *ApiStore

	mounters       *MounterGroup
	csiMode        CsiPluginMode
	selinuxSupport bool
	config         *DriverConfig
	manager        ctrl.Manager // controller-runtime manager for K8s client access
	isLeader       atomic.Bool  // tracks current leadership state for health checks
}

func NewWekaFsDriver(
	driverName, nodeID, endpoint string, maxVolumesPerNode int64, version string,
	csiMode CsiPluginMode, selinuxSupport bool, config *DriverConfig) (*WekaFsDriver, error) {
	if driverName == "" {
		return nil, errors.New("no driver name provided")
	}

	if nodeID == "" {
		return nil, errors.New("no node id provided")
	}

	if endpoint == "" {
		return nil, errors.New("no driver endpoint provided")
	}
	if version != "" {
		vendorVersion = version
	}

	log.Info().Msg(fmt.Sprintf("Driver: %v ", driverName))
	log.Info().Msg(fmt.Sprintf("Version: %s", vendorVersion))

	log.Info().Msg(fmt.Sprintf("csiMode: %s", csiMode))
	config.Log()

	return &WekaFsDriver{
		name:              driverName,
		nodeID:            nodeID,
		version:           vendorVersion,
		endpoint:          endpoint,
		maxVolumesPerNode: maxVolumesPerNode,
		api:               NewApiStore(config, nodeID),
		csiMode:           csiMode, // either "controller", "node", "all"
		selinuxSupport:    selinuxSupport,
		config:            config,
	}, nil
}

func (driver *WekaFsDriver) Run(ctx context.Context) {

	driver.initManager(ctx)

	if driver.csiMode != CsiModeMetricsServer {
		driver.mounters = NewMounterGroup(ctx, driver)

		log.Info().Msg("Loading IdentityServer")
		driver.ids = NewIdentityServer(driver)

	} else {
		log.Info().Msg("Running in Metrics Server mode, skipping IdentityServer and MounterGroup initialization")
		driver.ids = nil
		driver.mounters = nil
	}
	// Create GRPC servers

	if driver.csiMode == CsiModeController || driver.csiMode == CsiModeAll {
		log.Info().Msg("Loading ControllerServer")
		// bring up controller part
		driver.cs = NewControllerServer(driver)
	} else {
		driver.cs = &ControllerServer{}
	}

	if driver.csiMode == CsiModeNode || driver.csiMode == CsiModeAll {

		// only if we manage node labels, first clean up before starting node server
		if driver.config.manageNodeTopologyLabels {
			log.Info().Msg("Cleaning up node stale labels")
			driver.CleanupNodeLabels(ctx)
		}

		log.Info().Msg("Loading NodeServer")
		driver.ns = NewNodeServer(driver)
	} else {
		driver.ns = &NodeServer{}
	}

	if driver.csiMode == CsiModeMetricsServer || driver.csiMode == CsiModeAll {
		log.Info().Msg("Loading MetricsServer")
		driver.ms = NewMetricsServer(driver)
		driver.ms.Start(ctx)
	}

	s := NewNonBlockingGRPCServer(driver.csiMode)

	termContext, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Controller mode with manager: use leader election
	// Controller mode without manager (not in K8s): run without leader election
	// Node-only mode: run without leader election
	if (driver.csiMode == CsiModeController || driver.csiMode == CsiModeAll) && driver.manager != nil {
		driver.runWithLeaderElection(ctx, termContext, s)
	} else {
		driver.runWithoutLeaderElection(ctx, termContext, s)
	}
}

// runWithLeaderElection runs the controller with leader election enabled.
// Only the leader starts the gRPC server; standby pods wait for leadership.
func (driver *WekaFsDriver) runWithLeaderElection(ctx context.Context, termContext context.Context, s *nonBlockingGRPCServer) {
	log.Info().Msg("Running controller with leader election")

	runCtx, cancelRun := context.WithCancel(ctx)
	var shutdownOnce sync.Once

	// Add runnable that starts gRPC server when we become leader
	if driver.csiMode != CsiModeMetricsServer {
		err := driver.manager.Add(manager.RunnableFunc(func(ctx context.Context) error {
			// This only runs when we are the leader
			log.Info().Msg("Became leader - starting gRPC server")

			s.Start(driver.endpoint, driver.ids, driver.cs, driver.ns)

			// Mark as leader for health checks
			driver.isLeader.Store(true)

			// Signal to sidecars that we are the leader
			if err := createLeaderReadyFile(); err != nil {
				log.Error().Err(err).Msg("Failed to create leader ready file")
			}

			// Wait for context cancellation (leadership lost or shutdown)
			<-ctx.Done()

			log.Info().Msg("Leadership lost or shutdown - stopping gRPC server")

			// Mark as not leader for health checks
			driver.isLeader.Store(false)

			// Remove leader ready file before stopping gRPC
			if err := removeLeaderReadyFile(); err != nil {
				log.Error().Err(err).Msg("Failed to remove leader ready file")
			}

			s.Stop() // GracefulStop blocks until in-flight RPCs complete
			// Lease is held until this function returns (LeaderElectionReleaseOnCancel: true)

			return nil
		}))
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to add gRPC runnable to manager")
		}
	}

	// Handle termination signal
	go func() {
		<-termContext.Done()
		log.Info().Msg("Received termination signal")

		shutdownOnce.Do(func() {
			if (driver.csiMode == CsiModeNode || driver.csiMode == CsiModeAll) &&
				driver.config.manageNodeTopologyLabels {
				driver.CleanupNodeLabels(ctx)
			}

			if driver.csiMode == CsiModeController || driver.csiMode == CsiModeAll {
				log.Info().Msg("Waiting for background tasks to complete")
				driver.cs.getBackgroundTasksWg().Wait()
				log.Info().Msg("Background tasks completed")
			}

			cancelRun()
		})
	}()

	// Start manager (blocks until shutdown)
	log.Info().Msg("Starting manager - waiting for leadership")
	if err := driver.manager.Start(runCtx); err != nil {
		log.Error().Err(err).Msg("Manager exited with error")
	}

	s.Wait()
	log.Info().Msg("Shutdown complete")
}

// runWithoutLeaderElection runs the driver without leader election (for node-only mode)
func (driver *WekaFsDriver) runWithoutLeaderElection(ctx context.Context, termContext context.Context, s *nonBlockingGRPCServer) {
	log.Info().Msg("Running without leader election (node-only mode)")

	go func() {
		<-termContext.Done()
		if (driver.csiMode == CsiModeNode || driver.csiMode == CsiModeAll) &&
			driver.config.manageNodeTopologyLabels {
			log.Info().Msg("Cleanup of node labels...")
			driver.CleanupNodeLabels(ctx)
			log.Info().Msg("Cleanup of node labels completed")
		}
		if driver.csiMode == CsiModeController || driver.csiMode == CsiModeAll {
			log.Info().Msg("Waiting for background tasks to complete")
			driver.cs.getBackgroundTasksWg().Wait()
			log.Info().Msg("Background tasks completed")
		}

		s.Stop()
		log.Info().Msg("Server stopped")
		os.Exit(1)
	}()
	if s.csiMode != CsiModeMetricsServer {
		s.Start(driver.endpoint, driver.ids, driver.cs, driver.ns)
		s.Wait()
	}
	if driver.csiMode == CsiModeMetricsServer {
		driver.ms.Wait()
	}

}

func (d *WekaFsDriver) SetNodeLabels(ctx context.Context) {
	if d.csiMode != CsiModeNode && d.csiMode != CsiModeAll {
		return
	}
	client := d.manager.GetClient()
	if client == nil {
		log.Error().Msg("Failed to get Kubernetes client")
		return
	}
	node := &v1.Node{}

	key := types.NamespacedName{
		Name: d.nodeID,
	}

	err := client.Get(ctx, key, node)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get node object from Kubernetes")
		return
	}

	transport := func() string {
		if d.config.useNfs {
			return "nfs"
		}
		wekaRunning := isWekaRunning(ctx)
		if d.config.allowNfsFailback && !wekaRunning {
			return "nfs"
		}
		return "wekafs"
	}()

	labelsToSet := make(map[string]string)
	labelsToSet[TopologyKeyNode] = d.nodeID
	labelsToSet[fmt.Sprintf(TopologyLabelNodePattern, d.name)] = d.nodeID
	labelsToSet[fmt.Sprintf(TopologyLabelWekaLocalPattern, d.name)] = "true"
	labelsToSet[fmt.Sprintf(TopologyLabelTransportPattern, d.name)] = transport
	updateNeeded := false

	for label, value := range labelsToSet {
		existing, ok := node.Labels[label]
		if !ok || existing != value {
			log.Info().Str("label", fmt.Sprintf("%s=%s", label, value)).Str("node", node.Name).Msg("Setting label on node")
			node.Labels[label] = value
			updateNeeded = true
		}
	}

	if !updateNeeded {
		return
	}
	err = client.Update(ctx, node)
	if err != nil {
		log.Error().Err(err).Msg("Failed to update node labels")
		return
	}

	log.Info().Msg("Successfully updated labels on node")
}

func (d *WekaFsDriver) CleanupNodeLabels(ctx context.Context) {
	if d.csiMode != CsiModeNode && d.csiMode != CsiModeAll {
		return
	}
	nodeLabelPatternsToRemove := []string{TopologyLabelNodePattern, TopologyLabelTransportPattern, TopologyLabelWekaLocalPattern}
	nodeLabelsToRemove := []string{TopologyLabelTransportGlobal, TopologyLabelNodeGlobal, TopologyKeyNode}

	for i, labelPattern := range nodeLabelPatternsToRemove {
		nodeLabelPatternsToRemove[i] = fmt.Sprintf(labelPattern, d.name)
	}
	labelsToRemove := append(nodeLabelsToRemove, nodeLabelPatternsToRemove...)

	client := d.manager.GetClient()
	if client == nil {
		log.Error().Msg("Failed to get Kubernetes client")
		return
	}

	node := &v1.Node{}
	key := types.NamespacedName{Name: d.nodeID}
	err := client.Get(ctx, key, node)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get node")
		return
	}

	for _, label := range labelsToRemove {
		delete(node.Labels, label)
		log.Info().Str("label", label).Str("node", node.Name).Msg("Removing label from node")
	}

	err = client.Update(ctx, node)
	if err != nil {
		log.Error().Err(err).Msg("Failed to update node labels")
		return
	}

	log.Info().Msg("Successfully removed labels from node")
}

// initManager initializes the controller-runtime manager with leader election for controller mode
func (d *WekaFsDriver) initManager(ctx context.Context) error {
	logger := log.Ctx(ctx).With().Str("component", "manager-init").Logger()

	leaderElectionID := fmt.Sprintf("%s-controller-leader", d.name)

	// Get health port from environment variable
	healthPort := ""
	switch d.csiMode {
	case CsiModeMetricsServer:
		healthPort = os.Getenv("READINESS_PORT")
		if healthPort == "" {
			healthPort = HealthProbePort
		}
		leaderElectionID = "csimetricsad0b5146.weka.io" // to match with existing deployments

	case CsiModeController:
		healthPort = os.Getenv("HEALTH_PORT")
		if healthPort == "" {
			healthPort = HealthProbePort // Default to 8081
		}
	default:
		healthPort = HealthProbePort

	}
	logger.Info().Str("readiness_port", healthPort).Msg("Using default readiness port")

	// Get Kubernetes config
	config, err := rest.InClusterConfig()
	if err != nil {
		if errors.Is(err, rest.ErrNotInCluster) {
			log.Error().Msg("Not running in a Kubernetes cluster, trying to fetch default kubeconfig")
			// Fallback to using kubeconfig from the local environment
			kubeconfig := os.Getenv("KUBECONFIG")
			if kubeconfig == "" {
				log.Error().Msg("KUBECONFIG environment variable is not set")
				Die("KUBECONFIG environment variable is not set, cannot run MetricsServer without it and not in cluster")
			}
			config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
			if err != nil {
				log.Error().Err(err).Msg("Failed to create config from kubeconfig file")
				Die("Failed to create config from kubeconfig file, cannot run MetricsServer without it")
			}
		} else {
			log.Error().Err(err).Msg("Failed to create in-cluster config")
			Die("Failed to create in-cluster config, cannot run MetricsServer without it")
		}
	}

	// Get namespace for leader election
	namespace, err := getOwnNamespace()
	if err != nil {
		return fmt.Errorf("failed to get namespace for leader election: %w", err)
	}

	// Create scheme and register core v1 types
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	pprofBindAddress := os.Getenv("PPROF_BIND_ADDRESS")
	if pprofBindAddress != "" {
		logger.Info().Str("pprof_bind_address", pprofBindAddress).Msg("Using PPROF_BIND_ADDRESS environment variable for pprof binding address")
	}

	// Setup logger for controller-runtime
	zapLogger := zap.New(zap.UseDevMode(false))
	crlog.SetLogger(zapLogger)


	// Configure cache options (only if enforceDirVolTotalCapacity is enabled)
	cacheOpts := cache.Options{}
	if d.config.enforceDirVolTotalCapacity {
		cacheOpts = cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&v1.PersistentVolume{}: {
					Transform: stripUnnecessaryPVFields,
				},
			},
		}
	}

	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                        scheme,
		LeaderElection:                true,
		LeaderElectionNamespace:       namespace,
		LeaderElectionID:              leaderElectionID,
		LeaderElectionReleaseOnCancel: true,
		Cache:                         cacheOpts,
		HealthProbeBindAddress:        ":" + healthPort,
		PprofBindAddress:              pprofBindAddress,
	})
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	// only for CSI, add actual healthcheck on CSI Unix Domain Socket
	socketHealtcheck := false
	if d.csiMode != CsiModeMetricsServer {
		// Parse socket path from endpoint (format: "unix:///path/to/socket")
		socketProto, socketPath, err := parseEndpoint(d.endpoint)
		if err != nil {
			return fmt.Errorf("failed to parse endpoint for health check: %w", err)
		}
		if socketProto == "unix" {
			socketPath = "/" + socketPath // parseEndpoint strips leading slash
		}

		// - Standby: OK (process is alive)
		// - Leader: verify gRPC server accepts connections + Weka client running (if needed)
		if err := mgr.AddHealthzCheck("healthz", func(_ *http.Request) error {
			if d.isLeader.Load() {
				// Leader: verify gRPC server accepts connections
				conn, err := net.DialTimeout(socketProto, socketPath, time.Second)
				if err != nil {
					return fmt.Errorf("gRPC server not accessible: %w", err)
				}
				_ = conn.Close()

				if !d.config.useNfs && !d.config.allowNfsFailback {
					if !isWekaRunning(ctx) {
						return fmt.Errorf("weka client not running on leader node")
					}
				}
				return nil
			}
			// Standby: alive is enough
			return nil
		}); err != nil {
			return fmt.Errorf("failed to add health check: %w", err)
		}
		socketHealtcheck = true
	}

	d.manager = mgr
	logger.Info().
		Str("leader_election_id", leaderElectionID).
		Str("namespace", namespace).
		Str("health_probe_port", healthPort).
		Bool("enforce_capacity", d.config.enforceDirVolTotalCapacity).
		Bool("healthcheck_csi_socket", socketHealtcheck).
		Msg("Kubernetes manager initialized with leader election")
	return nil
}
