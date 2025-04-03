package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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

	ids            *identityServer
	ns             *NodeServer
	cs             *ControllerServer
	api            *ApiStore
	mounters       *MounterGroup
	csiMode        CsiPluginMode
	selinuxSupport bool
	config         *DriverConfig
	k8sApiClient   *kubernetes.Clientset
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

	driver.mounters = NewMounterGroup(driver)
	// Create GRPC servers

	// identity server runs always
	log.Info().Msg("Loading IdentityServer")
	driver.ids = NewIdentityServer(driver)

	if driver.csiMode == CsiModeController || driver.csiMode == CsiModeAll {
		log.Info().Msg("Loading ControllerServer")
		// bring up controller part
		driver.cs = NewControllerServer(driver)
	} else {
		driver.cs = &ControllerServer{}
	}

	if driver.csiMode == CsiModeNode || driver.csiMode == CsiModeAll {

		// bring up node part
		log.Info().Msg("Cleaning up node stale labels")
		driver.CleanupNodeLabels(ctx)
		log.Info().Msg("Loading NodeServer")
		driver.ns = NewNodeServer(driver)
	} else {
		driver.ns = &NodeServer{}
	}

	s := NewNonBlockingGRPCServer(driver.csiMode)

	termContext, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() {
		<-termContext.Done()
		if driver.csiMode == CsiModeNode || driver.csiMode == CsiModeAll {
			log.Info().Msg("Received SIGTERM/SIGINT, running cleanup of node labels...")
			driver.CleanupNodeLabels(ctx)
			log.Info().Msg("Cleanup completed, stopping server")
		} else {
			log.Info().Msg("Received SIGTERM/SIGINT, stopping server")
		}
		s.Stop()
		log.Info().Msg("Server stopped")
		os.Exit(1)

	}()

	s.Start(driver.endpoint, driver.ids, driver.cs, driver.ns)
	s.Wait()
}

func (d *WekaFsDriver) GetK8sApiClient() *kubernetes.Clientset {
	if d.k8sApiClient == nil {
		config, err := rest.InClusterConfig()
		if err != nil {
			if errors.Is(err, rest.ErrNotInCluster) {
				log.Error().Msg("Not running in a Kubernetes cluster, trying to fetch default kubeconfig")
				return nil
			}
			log.Error().Err(err).Msg("Failed to create in-cluster config")
			return nil
		}

		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			log.Error().Err(err).Msg("Failed to create Kubernetes client")
			return nil
		}
		d.k8sApiClient = clientset
	}
	return d.k8sApiClient
}

func (d *WekaFsDriver) SetNodeLabels(ctx context.Context) {
	if d.csiMode != CsiModeNode && d.csiMode != CsiModeAll {
		return
	}
	client := d.GetK8sApiClient()
	if client == nil {
		log.Error().Msg("Failed to get Kubernetes client")
		return
	}
	node, err := client.CoreV1().Nodes().Get(ctx, d.nodeID, v1.GetOptions{})
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

	_, err = d.GetK8sApiClient().CoreV1().Nodes().Update(ctx, node, v1.UpdateOptions{})
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

	client := d.GetK8sApiClient()
	if client == nil {
		log.Error().Msg("Failed to get Kubernetes client")
		return
	}

	node, err := client.CoreV1().Nodes().Get(ctx, d.nodeID, v1.GetOptions{})
	if err != nil {
		log.Error().Err(err).Msg("Failed to get node")
		return
	}

	for _, label := range labelsToRemove {
		delete(node.Labels, label)
		log.Info().Str("label", label).Str("node", node.Name).Msg("Removing label from node")
	}

	_, err = client.CoreV1().Nodes().Update(ctx, node, v1.UpdateOptions{})
	if err != nil {
		log.Error().Err(err).Msg("Failed to update node labels")
		return
	}

	log.Info().Msg("Successfully removed labels from node")
}
