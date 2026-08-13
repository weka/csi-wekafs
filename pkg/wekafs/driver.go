/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package wekafs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	clog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var vendorVersion = "dev"

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
	// Shared by the controller and node servers, and re-evaluated by Probe as the Weka client comes
	// and goes on the host.
	mounters       *MounterGroup
	api            *ApiStore
	debugPath      string
	csiMode        CsiPluginMode
	selinuxSupport bool
	config         *DriverConfig
	manager        ctrl.Manager // controller-runtime manager for K8s client access
	isLeader       atomic.Bool  // tracks current leadership state for health checks
}

func NewWekaFsDriver(
	driverName, nodeID, endpoint string, maxVolumesPerNode int64, version, debugPath string,
	csiMode CsiPluginMode, selinuxSupport bool, config *DriverConfig) (*WekaFsDriver, error) {
	if driverName == "" {
		return nil, errors.New("no driver name provided")
	}

	if nodeID == "" {
		return nil, errors.New("no node id provided")
	}

	// A mode with no CSI gRPC surface has no endpoint to listen on, and must not be made to invent a
	// placeholder one just to satisfy this check.
	if endpoint == "" && csiMode.servesCsiGrpc() {
		return nil, errors.New("no driver endpoint provided")
	}
	if version != "" {
		vendorVersion = version
	}

	log.Info().Msg(fmt.Sprintf("Driver: %v ", driverName))
	log.Info().Msg(fmt.Sprintf("Version: %s", vendorVersion))

	log.Info().Msg(fmt.Sprintf("csiMode: %s", csiMode))
	config.Log()

	driver := &WekaFsDriver{
		name:              driverName,
		nodeID:            nodeID,
		version:           vendorVersion,
		endpoint:          endpoint,
		maxVolumesPerNode: maxVolumesPerNode,
		api:               NewApiStore(config, nodeID, driverName),
		debugPath:         debugPath,
		csiMode:           csiMode, // either "controller", "node", or "metricsserver"
		selinuxSupport:    selinuxSupport,
		config:            config,
	}

	// The metrics server lists PersistentVolumes cluster-wide through the controller-runtime manager,
	// so it is only ever constructed for the mode that runs it - CsiModeMetricsServer (its own
	// Deployment). It must never be constructed merely because the controller or node service is
	// running, and never for a node-only pod, which would otherwise list the same cluster-wide PVs
	// from every node. Constructing it here rather than lazily in Run() lets main.go register its
	// Prometheus collectors right after the driver is built, before Run() blocks for the lifetime of
	// the process.
	//
	// A CsiModeMetricsServer pod exists only to export metrics, so one that came up without a metrics
	// server would sit there looking healthy while collecting nothing - fail instead, and let the
	// Deployment surface it.
	if csiMode == CsiModeMetricsServer {
		ms, err := NewMetricsServer(driver)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize metrics server: %w", err)
		}
		driver.ms = ms
	}

	return driver, nil
}

func (driver *WekaFsDriver) Run(ctx context.Context) {
	// cleanup of stale leader file on container crash/restart
	if driver.csiMode == CsiModeController {
		if err := removeLeaderReadyFile(); err != nil {
			log.Warn().Err(err).Msg("Failed to remove stale leader ready file on startup")
		}
	}

	// Without a CSI gRPC surface there are no volumes to mount and no Identity service to answer, so
	// skip building the mounters and the IdentityServer. One group is built for the whole process and
	// shared by both servers, so a filesystem mounted by one is the same mount the other sees.
	var mounters *MounterGroup
	if driver.csiMode.servesCsiGrpc() {
		mounters = NewMounterGroup(ctx, driver)
		driver.mounters = mounters

		// Create servers
		log.Info().Msg("Loading IdentityServer")
		driver.ids = NewIdentityServer(driver.name, driver.version, driver.config)
	}

	if driver.csiMode == CsiModeController {
		log.Info().Msg("Loading ControllerServer")

		// Initialize manager with leader election
		if err := driver.initManager(ctx, true); err != nil {
			log.Warn().Err(err).Msg("Failed to initialize Kubernetes manager, running without leader election")
		}

		driver.cs = NewControllerServer(driver.nodeID, driver.api, mounters, driver.config, driver.manager)
	} else {
		driver.cs = &ControllerServer{}
	}

	if driver.csiMode == CsiModeNode {
		// only if we manage node labels, first clean up before starting node server
		if driver.config.manageNodeTopologyLabels {
			log.Info().Msg("Cleaning up node stale labels")
			driver.CleanupNodeLabels(ctx)
		}

		// The controller block above didn't run in node mode, so the manager (and its embedded K8s
		// client) is not yet initialized. Initialize it now without leader election – the node server
		// only needs the client to read PVC/Pod annotations for per-pod mount option overrides.
		if err := driver.initManager(ctx, false); err != nil {
			log.Warn().Err(err).Msg("Failed to initialize Kubernetes client for node mode, per-pod mount option overrides will be unavailable")
		}

		log.Info().Msg("Loading NodeServer")
		driver.ns = NewNodeServer(driver.nodeID, driver.maxVolumesPerNode, driver.api, mounters, driver.config)

		// Read zone/region from node labels at startup so they are available
		// when NodeGetInfo is called during registration (e.g. after CSI upgrade).
		// This only needs to happen once — these labels are set by the cloud provider
		// and don't change at runtime.
		driver.readNodeTopologyLabels(ctx)
	} else {
		driver.ns = &NodeServer{}
	}

	// Metrics-server-only mode has no ControllerServer to bring up a manager for, but it still needs
	// one for Kubernetes access and (optionally) leader election - the same mechanism controller mode
	// uses. It never registers a gRPC surface though; see runWithLeaderElection/runWithoutLeaderElection.
	// Unlike controller mode, there is no useful degraded mode to fall back to here: without a manager
	// the metrics server has no Kubernetes access, so it would discover no PersistentVolumes and export
	// nothing at all. Fail rather than idle.
	if driver.csiMode == CsiModeMetricsServer {
		if err := driver.initManager(ctx, true); err != nil {
			log.Fatal().Err(err).Msg("Failed to initialize Kubernetes manager, metrics server cannot run")
		}
	}

	s := NewNonBlockingGRPCServer(driver.csiMode)

	termContext, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Controller/metrics-server mode with manager: use leader election
	// Controller mode without manager (not in K8s): run without leader election
	// Node-only mode: run without leader election
	if (driver.csiMode == CsiModeController || driver.csiMode == CsiModeMetricsServer) && driver.manager != nil {
		driver.runWithLeaderElection(ctx, termContext, s)
	} else {
		driver.runWithoutLeaderElection(ctx, termContext, s)
	}
}

// leaderElectionIDForMode names the lease a mode competes for. The metrics server gets its own,
// because it elects a leader for an unrelated reason: one replica should collect metrics, which says
// nothing about which replica should serve the CSI controller service.
//
// Sharing one lease made them compete. With the metrics server enabled inside the plugin release,
// its pods could win the controller's lease, leaving both controller pods waiting for a leadership
// they would never get: the gRPC server never started, wait-for-leader gated every sidecar forever,
// and provisioning stopped while every pod still reported healthy.
//
// The controller's lease name is deliberately unchanged. Renaming it would have old and new pods
// electing separate leaders from the same release during a rolling upgrade.
func leaderElectionIDForMode(driverName string, mode CsiPluginMode) string {
	if mode == CsiModeMetricsServer {
		return fmt.Sprintf("%s-metricsserver-leader", driverName)
	}
	return fmt.Sprintf("%s-controller-leader", driverName)
}

// initManager initializes the controller-runtime manager.
// Pass leaderElection=true for controller mode (acquires a lease before serving).
// Pass leaderElection=false for node mode (client-only, no lease required).
func (d *WekaFsDriver) initManager(ctx context.Context, leaderElection bool) error {
	logger := log.Ctx(ctx).With().Str("component", "manager-init").Logger()

	// Get Kubernetes config
	config, err := rest.InClusterConfig()
	if err != nil {
		if errors.Is(err, rest.ErrNotInCluster) {
			logger.Warn().Msg("Not running in cluster, trying KUBECONFIG")
			kubeconfig := os.Getenv("KUBECONFIG")
			if kubeconfig == "" {
				return fmt.Errorf("not in cluster and KUBECONFIG not set")
			}
			config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
			if err != nil {
				return fmt.Errorf("failed to build config from kubeconfig: %w", err)
			}
		} else {
			return fmt.Errorf("failed to get in-cluster config: %w", err)
		}
	}

	// Create scheme and register core v1 types
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	// Setup logger for controller-runtime
	zapLogger := zap.New(zap.UseDevMode(false))
	clog.SetLogger(zapLogger)

	byObject := map[runtimeclient.Object]cache.ByObject{}

	// When PVs are cached at all, keep only the fields the driver reads, so a large cluster's PV
	// list stays cheap in memory.
	if d.config.requiresPvCaching() {
		byObject[&v1.PersistentVolume{}] = cache.ByObject{
			Transform: stripUnnecessaryPVFields,
		}
	}

	// The node server reads its own Node object to reconcile topology labels. controller-runtime
	// starts an informer lazily on the first Get of a type, so without this the node pod would
	// LIST and WATCH every Node in the cluster - on a large fleet that is one full Node cache per
	// node pod. Scope it to this node alone; no other role touches Node objects.
	if d.csiMode == CsiModeNode {
		byObject[&v1.Node{}] = cache.ByObject{
			Field: fields.OneTermEqualSelector("metadata.name", d.nodeID),
		}
	}

	cacheOpts := cache.Options{}
	if len(byObject) > 0 {
		cacheOpts.ByObject = byObject
	}

	mgrOpts := ctrl.Options{
		Scheme: scheme,
		Cache:  cacheOpts,
		// Node mode: disable both HTTP servers to avoid port conflicts.
		// Controller and node pods share the host network namespace (hostNetwork: true),
		// so any port the node manager binds would block the controller manager from using it.
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "",
	}

	if leaderElection {
		// Controller mode: bind health probe so Kubernetes liveness probes work.
		// Override the node-mode defaults set above.
		healthPort := os.Getenv("HEALTH_PORT")
		if healthPort == "" {
			healthPort = HealthProbePort
		}
		mgrOpts.HealthProbeBindAddress = ":" + healthPort

		// Get namespace for leader election lease
		namespace, err := getOwnNamespace()
		if err != nil {
			return fmt.Errorf("failed to get namespace for leader election: %w", err)
		}
		mgrOpts.LeaderElection = true
		mgrOpts.LeaderElectionNamespace = namespace
		mgrOpts.LeaderElectionID = leaderElectionIDForMode(d.name, d.csiMode)
		mgrOpts.LeaderElectionReleaseOnCancel = true
	}

	mgr, err := ctrl.NewManager(config, mgrOpts)
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	// ControllerGetVolume resolves a CSI volume handle back to its PersistentVolume on every
	// health check. Index the handle so that is a cache lookup rather than a full PV scan.
	// Registering the index starts a PV informer, so only do it where it is actually used - keyed
	// on the csiMode that serves the controller service, which is also what gates advertising the
	// capability, rather than on leaderElection which merely happens to correlate today.
	servesControllerService := d.csiMode == CsiModeController
	if servesControllerService && d.config.advertiseVolumeHealthSupport {
		if err := mgr.GetFieldIndexer().IndexField(ctx, &v1.PersistentVolume{}, pvIndexVolumeHandle,
			func(obj runtimeclient.Object) []string {
				pv, ok := obj.(*v1.PersistentVolume)
				if !ok || pv.Spec.CSI == nil {
					return nil
				}
				return []string{pv.Spec.CSI.VolumeHandle}
			}); err != nil {
			return fmt.Errorf("failed to index persistent volumes by CSI volume handle: %w", err)
		}
	}

	// This probe reports on the gRPC server, so it belongs only to the modes that run one. A metrics
	// server has no endpoint to parse or dial; AddToManager registers its own healthz instead.
	if leaderElection && d.csiMode.servesCsiGrpc() {
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
		if err := mgr.AddHealthzCheck("healthz", func(r *http.Request) error {
			if d.isLeader.Load() {
				// Leader: verify gRPC server accepts connections
				conn, err := net.DialTimeout(socketProto, socketPath, time.Second)
				if err != nil {
					return fmt.Errorf("gRPC server not accessible: %w", err)
				}
				_ = conn.Close()

				if !d.config.useNfs && !d.config.allowNfsFailback && !d.config.isInDevMode() {
					wekaCtx, wekaCancel := context.WithTimeout(r.Context(), d.config.healthProbeWekaTimeout)
					defer wekaCancel()
					if !isWekaRunning(wekaCtx) {
						return fmt.Errorf("weka client not running or unresponsive")
					}
				}
				return nil
			}
			// Standby: alive is enough
			return nil
		}); err != nil {
			return fmt.Errorf("failed to add health check: %w", err)
		}
	}

	d.manager = mgr
	logger.Info().
		Bool("leader_election", leaderElection).
		Bool("enforce_capacity", d.config.enforceDirVolTotalCapacity).
		Bool("advertise_volume_health_support", d.config.advertiseVolumeHealthSupport).
		Bool("cache_persistent_volumes", d.config.requiresPvCaching()).
		Str("leader_election_id", mgrOpts.LeaderElectionID).
		Str("namespace", mgrOpts.LeaderElectionNamespace).
		Msg("Kubernetes manager initialized")
	return nil
}
