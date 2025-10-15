package wekafs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-logr/zerologr"
	"github.com/rs/zerolog/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	clog "sigs.k8s.io/controller-runtime/pkg/log"
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

	manager ctrl.Manager

	mounters       *MounterGroup
	csiMode        CsiPluginMode
	selinuxSupport bool
	config         *DriverConfig
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
		driver.mounters = NewMounterGroup(driver)

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

	s := NewNonBlockingGRPCServer(driver.csiMode, driver.config)

	termContext, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() {
		<-termContext.Done()
		log.Info().Msg("Received SIGTERM/SIGINT, stopping server")
		if driver.csiMode == CsiModeController || driver.csiMode == CsiModeAll {
			log.Info().Msg("Waiting for background tasks to complete")
			driver.cs.getBackgroundTasksWg().Wait()
			log.Info().Msg("Background tasks completed")
		}
		if driver.csiMode == CsiModeNode || driver.csiMode == CsiModeAll && driver.config.manageNodeTopologyLabels {
			log.Info().Msg("Running cleanup of node labels")
			driver.CleanupNodeLabels(ctx)
			log.Info().Msg("Cleanup of node labels completed")
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
	if d.manager == nil {
		log.Error().Msg("Manager is not initialized, cannot cleanup node labels")
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
		wekaRunning := isWekaRunning()
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
	if d.manager == nil {
		log.Error().Msg("Manager is not initialized, cannot cleanup node labels")
		return
	}
	client := d.manager.GetClient()
	if client == nil {
		log.Error().Msg("Failed to get Kubernetes client")
		return
	}

	if d.csiMode != CsiModeNode && d.csiMode != CsiModeAll {
		return
	}
	nodeLabelPatternsToRemove := []string{TopologyLabelNodePattern, TopologyLabelTransportPattern, TopologyLabelWekaLocalPattern}
	nodeLabelsToRemove := []string{TopologyLabelTransportGlobal, TopologyLabelNodeGlobal, TopologyKeyNode}

	for i, labelPattern := range nodeLabelPatternsToRemove {
		nodeLabelPatternsToRemove[i] = fmt.Sprintf(labelPattern, d.name)
	}
	labelsToRemove := append(nodeLabelsToRemove, nodeLabelPatternsToRemove...)

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

func (d *WekaFsDriver) initManager(ctx context.Context) {
	logger := log.Ctx(ctx)
	config, err := rest.InClusterConfig()
	if err != nil {
		if errors.Is(err, rest.ErrNotInCluster) {
			log.Error().Msg("Not running in a Kubernetes cluster, trying to fetch default kubeconfig")
			// Fallback to using kubeconfig from the local environment
			kubeconfig := os.Getenv("KUBECONFIG")
			if kubeconfig == "" {
				log.Error().Msg("KUBECONFIG environment variable is not set, failed to create K8s API config")
				return
			}
			config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
			if err != nil {
				log.Error().Err(err).Msg("Failed to create config from kubeconfig file")
				return
			}
		} else {
			logger.Error().Err(err).Msg("Failed to create K8s API config")
			return
		}
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	pprofBindAddress := os.Getenv("PPROF_BIND_ADDRESS")
	if pprofBindAddress != "" {
		logger.Info().Str("pprof_bind_address", pprofBindAddress).Msg("Using PPROF_BIND_ADDRESS environment variable for pprof binding address")
	}

	namespace, err := getOwnNamespace()
	if err != nil {
		logger.Error().Msg("Namespace not detected and not set, not using Leader Election mechanism")
	}
	zerologr.NameFieldName = "logger"
	zerologr.NameSeparator = "/"
	var logrLog = zerologr.New(logger)

	readinessPort := os.Getenv("READINESS_PORT")
	if readinessPort == "" {
		readinessPort = "8081" // Default port for readiness probe
		logger.Info().Str("readiness_port", readinessPort).Msg("Using default readiness port")
	}

	leaderElectionId := ""
	enableLeaderElection := false

	if d.csiMode == CsiModeMetricsServer || d.csiMode == CsiModeAll {
		leaderElectionId = "csiwekametricsad0b5146.weka.io"
		if d.config.enableMetricsServerLeaderElection {
			enableLeaderElection = true
		}
	}

	m, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                        scheme,
		LeaderElection:                enableLeaderElection,
		LeaderElectionNamespace:       namespace,
		LeaderElectionID:              leaderElectionId,
		LeaderElectionConfig:          nil,
		LeaderElectionReleaseOnCancel: true,
		HealthProbeBindAddress:        ":" + readinessPort,
		PprofBindAddress:              pprofBindAddress,
	})
	clog.SetLogger(logrLog)

	if err != nil {
		logger.Error().Err(err).Msg("unable to start manager")
		return
	}
	d.manager = m
}
