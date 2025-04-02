package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

var MountOptionsNotFoundInMap = errors.New("mount options not found in map")

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
	ms             *MetricsServer
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

	s := NewNonBlockingGRPCServer(driver.csiMode)

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

func (d *WekaFsDriver) GetK8sApiClient() *kubernetes.Clientset {
	if d.k8sApiClient == nil {
		config, err := rest.InClusterConfig()
		if err != nil {
			if errors.Is(err, rest.ErrNotInCluster) {
				log.Trace().Msg("Not running in a Kubernetes cluster, trying to fetch default kubeconfig")
				// Fallback to using kubeconfig from the local environment
				kubeconfig := os.Getenv("KUBECONFIG")
				if kubeconfig == "" {
					log.Error().Msg("KUBECONFIG environment variable is not set")
					return nil
				}
				config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
				if err != nil {
					log.Error().Err(err).Msg("Failed to create config from kubeconfig file")
					return nil
				}
			} else {
				log.Error().Err(err).Msg("Failed to create in-cluster config")
				return nil
			}
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
	node, err := client.CoreV1().Nodes().Get(ctx, d.nodeID, metav1.GetOptions{})
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

	_, err = d.GetK8sApiClient().CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
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

	node, err := client.CoreV1().Nodes().Get(ctx, d.nodeID, metav1.GetOptions{})
	if err != nil {
		log.Error().Err(err).Msg("Failed to get node")
		return
	}

	for _, label := range labelsToRemove {
		delete(node.Labels, label)
		log.Info().Str("label", label).Str("node", node.Name).Msg("Removing label from node")
	}

	_, err = client.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		log.Error().Err(err).Msg("Failed to update node labels")
		return
	}

	log.Info().Msg("Successfully removed labels from node")
}
func (d *WekaFsDriver) getMountOptionsConfigMap(ctx context.Context) (*v1.ConfigMap, error) {
	client := d.GetK8sApiClient()
	if client == nil {
		log.Error().Msg("Failed to get Kubernetes client")
		return nil, errors.New("failed to get Kubernetes client")
	}
	namespace, err := getOwnNamespace()
	if err != nil {
		log.Error().Err(err).Msg("Failed to get own namespace")
		return nil, err
	}

	configMap, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, getMountOptionsConfigMapName(d.name), metav1.GetOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			log.Info().Msg("Mount options config map not found, creating a new one")
			configMap = &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: getMountOptionsConfigMapName(d.name),
				},
			}
			return nil, nil
		}
		log.Error().Err(err).Msg("Failed to get config map")
		return nil, err
	}

	return configMap, nil
}

func (d *WekaFsDriver) GetVolumeMountOptionsFromMap(ctx context.Context, volumeName string) (string, error) {
	configMap, err := d.getMountOptionsConfigMap(ctx)
	if err != nil {
		return "", err
	}

	if configMap == nil {
		return "", nil
	}

	opts, ok := configMap.BinaryData[volumeName]
	if !ok {
		return "", MountOptionsNotFoundInMap
	}
	// now update the configmap with the new options

	// need to encrypt the options just for obfuscation purposes so only the driver can read them
	ret := string(SimpleXOR(opts))
	return ret, nil
}

func (d *WekaFsDriver) SetVolumeMountOptionsInMap(ctx context.Context, volumeName string, options string) error {
	client := d.GetK8sApiClient()
	if client == nil {
		log.Error().Msg("Failed to get Kubernetes client")
		return errors.New("failed to get Kubernetes client")
	}

	c, err := d.getMountOptionsConfigMap(ctx)
	if err != nil {
		return err
	}
	c.BinaryData[volumeName] = SimpleXOR([]byte(options))

	namespace, err := getOwnNamespace()
	if err != nil {
		log.Error().Err(err).Msg("Failed to get own namespace")
		return err
	}
	_, err = client.CoreV1().ConfigMaps(namespace).Update(ctx, c, metav1.UpdateOptions{})
	if err != nil {
		log.Error().Err(err).Msg("Failed to update config map")
		return err
	}
	return nil
}

func (d *WekaFsDriver) DeleteVolumeMountOptionsFromMap(ctx context.Context, volumeName string) {
	client := d.GetK8sApiClient()
	if client == nil {
		log.Error().Msg("Failed to get Kubernetes client")
		return
	}
	c, err := d.getMountOptionsConfigMap(ctx)
	if err != nil {
		return
	}
	if c == nil {
		return
	}
	delete(c.BinaryData, volumeName)

	namespace, err := getOwnNamespace()
	if err != nil {
		log.Error().Err(err).Msg("Failed to get own namespace")
		return
	}

	_, err = d.GetK8sApiClient().CoreV1().ConfigMaps(namespace).Update(ctx, c, metav1.UpdateOptions{})
	if err != nil {
		log.Error().Err(err).Msg("Failed to update config map")
		return
	}
}
