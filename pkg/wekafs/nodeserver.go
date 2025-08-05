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
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/mount-utils"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type NodeServer struct {
	csi.UnimplementedNodeServer
	caps              []*csi.NodeServiceCapability
	nodeID            string
	maxVolumesPerNode int64
	mounters          *MounterGroup
	api               *ApiStore
	config            *DriverConfig
	semaphores        map[string]*SemaphoreWrapper
	backgroundTasksWg *sync.WaitGroup // used to wait for background tasks to finish before shutting down the server

	metrics           *NodeServerMetrics
	sync.Mutex
}

func (ns *NodeServer) getBackgroundTasksWg() *sync.WaitGroup {
	return ns.backgroundTasksWg
}

func (ns *NodeServer) getNodeId() string {
	return ns.nodeID
}

func (ns *NodeServer) getDefaultMountOptions() MountOptions {
	return getDefaultMountOptions().MergedWith(NewMountOptionsFromString(NodeServerAdditionalMountOptions), ns.getConfig().mutuallyExclusiveOptions)
}

func (ns *NodeServer) getConfig() *DriverConfig {
	return ns.config
}

func (ns *NodeServer) getApiStore() *ApiStore {
	return ns.api
}

func (ns *NodeServer) getMounter(ctx context.Context) AnyMounter {
	return ns.mounters.GetPreferredMounter(ctx)
}

func (ns *NodeServer) getMounterByTransport(ctx context.Context, transport DataTransport) AnyMounter {
	return ns.mounters.GetMounterByTransport(ctx, transport)
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeExpandVolume(ctx context.Context, request *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	panic("implement me")
}

func (ns *NodeServer) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	volumeID := req.GetVolumeId()
	volumePath := req.GetVolumePath()
	ctx = context.WithValue(ctx, "startTime", time.Now())
	// Validate request fields
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "Volume ID must be provided")
	}
	if volumePath == "" {
		return nil, status.Error(codes.InvalidArgument, "Volume path must be provided")
	}
	if req.GetStagingTargetPath() != "" {
		if !PathExists(req.GetStagingTargetPath()) {
			return nil, status.Error(codes.NotFound, "Staging area path not found")
		}
	}

	if !PathIsWekaMount(ctx, volumePath) {
		return nil, status.Error(codes.NotFound, "Volume path not found")
	}

	// Validate Weka volume ID
	if err := validateVolumeId(volumeID); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid volume ID %s: %v", volumeID, err)
	}

	st := "FAILURE"
	defer func() {
		ns.metrics.Operations.GetVolumeStats.WithLabelValues(ns.getConfig().GetDriver().name, st).Inc()
		ns.metrics.Operations.GetVolumeStatsDuration.WithLabelValues(ns.getConfig().GetDriver().name, st).Observe(time.Since(ctx.Value("startTime").(time.Time)).Seconds())
	}()

	stats, err := getVolumeStats(volumePath)
	if err != nil || stats == nil {
		st = "SUCCESS"
		return &csi.NodeGetVolumeStatsResponse{
			Usage: nil,
			VolumeCondition: &csi.VolumeCondition{
				Abnormal: true,
				Message:  "Failed to fetch volume stats for volume",
			},
		}, status.Errorf(codes.Internal, "Failed to get stats for volume %s: %v", volumeID, err)
	}
	// Prepare response
	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Unit:      csi.VolumeUsage_BYTES,
				Total:     stats.TotalBytes,
				Used:      stats.UsedBytes,
				Available: stats.AvailableBytes,
			},
			{
				Unit:      csi.VolumeUsage_INODES,
				Total:     stats.TotalInodes,
				Used:      stats.UsedInodes,
				Available: stats.AvailableInodes,
			},
		},
		VolumeCondition: &csi.VolumeCondition{
			Abnormal: false,
			Message:  "volume is healthy",
		},
	}, nil
}

type VolumeStats struct {
	TotalBytes      int64
	UsedBytes       int64
	AvailableBytes  int64
	TotalInodes     int64
	UsedInodes      int64
	AvailableInodes int64
}

// getVolumeStats fetches filesystem statistics for the mounted volume path.
func getVolumeStats(volumePath string) (volumeStats *VolumeStats, err error) {
	var stat syscall.Statfs_t

	// Use Statfs to get filesystem statistics for the volume path
	err = syscall.Statfs(volumePath, &stat)
	if err != nil {
		return nil, err
	}

	// Calculate capacity, available, and used space in bytes
	capacityBytes := int64(stat.Blocks) * int64(stat.Bsize)
	availableBytes := int64(stat.Bavail) * int64(stat.Bsize)
	usedBytes := capacityBytes - availableBytes
	inodes := int64(stat.Files)
	inodesFree := int64(stat.Ffree)
	inodesUsed := inodes - inodesFree
	return &VolumeStats{capacityBytes, usedBytes, availableBytes, inodes, inodesUsed, inodesFree}, nil
}

func NewNodeServer(driver *WekaFsDriver) *NodeServer {
	if driver == nil {
		panic("Driver is nil")
	}
	//goland:noinspection GoBoolExpressions
	return &NodeServer{
		caps: getNodeServiceCapabilities(
			[]csi.NodeServiceCapability_RPC_Type{
				csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
				csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
				csi.NodeServiceCapability_RPC_VOLUME_CONDITION,
			},
		),
		nodeID:            driver.nodeID,
		maxVolumesPerNode: driver.maxVolumesPerNode,
		mounters:          driver.mounters,
		api:               driver.api,
		config:            driver.config,
		semaphores:        make(map[string]*SemaphoreWrapper),
		backgroundTasksWg: new(sync.WaitGroup),
		metrics:           NewNodeServerMetrics(),
	}
}

func (ns *NodeServer) acquireSemaphore(ctx context.Context, op string) (error, releaseSemaphore) {
	logger := log.Ctx(ctx)
	ns.initializeSemaphore(ctx, op)
	sem := ns.semaphores[op]

	logger.Trace().Msg("Acquiring semaphore")
	start := time.Now()
	err := sem.Acquire(ctx, 1)
	elapsed := time.Since(start)

	// select metrics histogram based on the operation type
	var histogram *prometheus.HistogramVec
	var gauge *prometheus.GaugeVec
	switch op {
	case "PublishVolume":
		histogram = ns.metrics.Concurrency.PublishVolumeWaitDuration
		gauge = ns.metrics.Concurrency.PublishVolume
	case "UnpublishVolume":
		histogram = ns.metrics.Concurrency.UnpublishVolumeWaitDuration
		gauge = ns.metrics.Concurrency.UnpublishVolume
	}
	driverName := ns.getConfig().GetDriver().name

	// update concurrent operations
	currentOps := func() {
		if gauge != nil {
			gauge.WithLabelValues(driverName, "acquired").Set(float64(sem.CurrentCount()))
		}
	}
	currentOps()

	if err == nil {
		logger.Trace().Dur("acquire_duration", elapsed).Str("op", op).Msg("Successfully acquired semaphore")
		if histogram != nil {
			histogram.WithLabelValues(driverName, "success").Observe(elapsed.Seconds())
		}
		return nil, func() {
			defer currentOps()
			elapsed = time.Since(start)
			logger.Trace().Dur("total_operation_time", elapsed).Str("op", op).Msg("Releasing semaphore")
			sem.Release(1)
		}
	}
	logger.Trace().Dur("acquire_duration", elapsed).Str("op", op).Msg("Failed to acquire semaphore")
	return err, func() {}
}

func (ns *NodeServer) initializeSemaphore(ctx context.Context, op string) {
	if _, ok := ns.semaphores[op]; ok {
		return
	}
	ns.Lock()
	defer ns.Unlock()

	if _, ok := ns.semaphores[op]; ok {
		return
	}

	m, ok := ns.getConfig().maxConcurrencyPerOp[op]
	if !ok { // if not set, default to 1
		m = 1
	}
	logger := log.Ctx(ctx)
	logger.Info().Str("op", op).Int64("max_concurrency", m).Msg("Initializing semaphore")
	sem := NewSemaphoreWrapper(m)

	ns.semaphores[op] = sem
}

func NodePublishVolumeError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.NodePublishVolumeResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error publishing volume")
	return &csi.NodePublishVolumeResponse{}, err
}

func (ns *NodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	op := "NodePublishVolume"
	volumeID := req.GetVolumeId()
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	ctx = context.WithValue(ctx, "startTime", time.Now())

	logger := log.Ctx(ctx)
	result := "FAILURE"

	logger.Info().Str("volume_id", volumeID).Msg(">>>> Received request")
	defer func() {
		level := zerolog.InfoLevel
		if result != "SUCCESS" {
			level = zerolog.ErrorLevel
		}

		ns.metrics.Operations.UnpublishVolumeDuration.WithLabelValues(ns.getConfig().GetDriver().name, result).Observe(time.Since(ctx.Value("startTime").(time.Time)).Seconds())
		ns.metrics.Operations.UnpublishVolume.WithLabelValues(ns.getConfig().GetDriver().name, result).Inc()

		logger.WithLevel(level).Str("result", result).Msg("<<<< Completed processing request")
	}()

	ctx, cancel := context.WithTimeout(ctx, ns.config.grpcRequestTimeout)
	err, dec := ns.acquireSemaphore(ctx, op)
	defer dec()
	defer cancel()
	if err != nil {
		return NodePublishVolumeError(ctx, codes.Unavailable, "Too many concurrent requests, please retry")
	}

	client, err := ns.api.GetClientFromSecrets(ctx, req.Secrets)
	if err != nil {
		return NodePublishVolumeError(ctx, codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}
	volume, err := NewVolumeFromId(ctx, req.GetVolumeId(), client, ns)
	if err != nil {
		return NodePublishVolumeError(ctx, codes.InvalidArgument, err.Error())
	}

	mountOptions := ns.fetchMountOptionsForRequest(ctx, req)
	logger.Trace().Str("mount_options", mountOptions).Msg("Updating volume mount options")
	volume.setMountOptions(ctx, NewMountOptionsFromString(mountOptions))
	volume.pruneUnsupportedMountOptions(ctx)

	// Check volume capabitily arguments
	if req.GetVolumeCapability() == nil {
		return NodePublishVolumeError(ctx, codes.InvalidArgument, "Volume capability missing in request")
	}
	if req.GetVolumeCapability().GetBlock() != nil &&
		req.GetVolumeCapability().GetMount() != nil {
		return NodePublishVolumeError(ctx, codes.InvalidArgument, "cannot have both block and Mount access type")
	}

	// check that requested capability is a mount
	if req.GetVolumeCapability().GetBlock() != nil {
		return NodePublishVolumeError(ctx, codes.InvalidArgument, "block volume mount not supported")
	}

	// check targetPath
	targetPath := filepath.Clean(req.GetTargetPath())
	mounter := mount.New("")
	if len(targetPath) == 0 {
		return NodePublishVolumeError(ctx, codes.InvalidArgument, "Target path missing in request")
	}

	fsType := req.GetVolumeCapability().GetMount().GetFsType()

	deviceId := ""
	if req.GetPublishContext() != nil {
		deviceId = req.GetPublishContext()[deviceID]
	}
	var innerMountOpts = []string{"bind"}

	readOnly := req.GetReadonly()
	// create a readonly mount
	if readOnly {
		volume.setMountOptions(ctx, NewMountOptionsFromString("ro"))
		innerMountOpts = append(innerMountOpts, "ro")
	}

	attrib := req.GetVolumeContext()
	mountFlags := req.GetVolumeCapability().GetMount().GetMountFlags()
	volume.mountOptions.Merge(NewMountOptionsFromString(strings.Join(mountFlags, ",")), ns.getConfig().mutuallyExclusiveOptions)

	logger.Debug().Str("target_path", targetPath).
		Str("fs_type", fsType).
		Str("device_id", deviceId).
		Bool("read_only", readOnly).
		Str("volume_id", volumeID).
		Fields(attrib).
		Str("mount_options", volume.mountOptions.String()).
		Str("mount_flags", strings.Join(mountFlags, ",")).
		Str("inner_mount_options", strings.Join(innerMountOpts, ",")).
		Msg("Performing underlying filesystem mount")

	err, unmount := volume.MountUnderlyingFS(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to mount underlying filesystem")
		return NodePublishVolumeError(ctx, codes.Internal, "Failed to mount a parent filesystem, check Authentication: "+err.Error())
	}
	defer unmount() // unmount the parent mount since there is a bind mount anyway

	fullPath := volume.GetFullPath(ctx)

	targetPathDir := filepath.Dir(targetPath)
	logger.Debug().Str("target_path", targetPathDir).Msg("Checking for path existence")

	if err = os.MkdirAll(targetPathDir, DefaultVolumePermissions); err != nil {
		return NodePublishVolumeError(ctx, codes.Internal, err.Error())
	}
	logger.Debug().Str("target_path", targetPath).Msg("Creating target path")
	if err = os.Mkdir(targetPath, 0750); err != nil {
		// If failed to create directory - other call succeded and not this one,
		// TODO: Returning success, but this is not completely right.
		// As potentially some other process holds. Need a good way to inspect binds
		// SearchMountPoints and GetMountRefs failed to do the job
		if os.IsExist(err) {
			if PathIsWekaMount(ctx, targetPath) {
				log.Ctx(ctx).Trace().Str("target_path", targetPath).Bool("weka_mounted", true).Msg("Target path exists")
				unmount()
				return &csi.NodePublishVolumeResponse{}, nil
			} else {
				log.Ctx(ctx).Trace().Str("target_path", targetPath).Bool("weka_mounted", false).Msg("Target path exists")
			}

		} else {
			log.Error().Err(err).Str("target_path", targetPath).Msg("Failed creating directory")
			unmount()
			return NodePublishVolumeError(ctx, codes.Internal, err.Error())
		}
	}
	logger.Debug().Str("volume_id", volumeID).Str("target_path", targetPath).Str("source_path", fullPath).
		Fields(innerMountOpts).Msg("Performing bind mount")

	// if we run in K8s isolated environment, 2nd mount must be done using mapped volume path
	if err := mounter.Mount(fullPath, targetPath, "", innerMountOpts); err != nil {
		logger.Error().Err(err).Str("full_path", fullPath).Str("target_path", targetPath).Msg("Failed to perform mount")
		return NodePublishVolumeError(ctx, codes.Internal, fmt.Sprintf("failed to Mount device: %s at %s: %s", fullPath, targetPath, err.Error()))
	}
	result = "SUCCESS"
	// Not doing unmount, NodePublish should do unmount but only when it unmounts bind successfully
	return &csi.NodePublishVolumeResponse{}, nil
}

func (ns *NodeServer) fetchMountOptionsForRequest(ctx context.Context, req *csi.NodePublishVolumeRequest) string {
	logger := log.Ctx(ctx)
	volumeId := req.GetVolumeId()

	// set volume mountOptions, try first from configmap:
	var mountOptions string
	var err error
	successInFetchingOptionsFromCM := false
	mountOptions, err = ns.getConfig().GetDriver().GetVolumeMountOptionsFromMap(ctx, volumeId)
	if err == nil {
		logger.Trace().Str("mount_options", mountOptions).Msg("Mount options fetched from config map")
		successInFetchingOptionsFromCM = true
	}
	// if could not fetch from config map, use volumeContext for backward compatibility
	if !successInFetchingOptionsFromCM {
		logger.Trace().Msg("Mount options not in config map, trying volumeContext")
		params := req.GetVolumeContext()
		mountOptions = params["mountOptions"]
	}
	return mountOptions

}

func NodeUnpublishVolumeError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.NodeUnpublishVolumeResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error unpublishing volume")
	return &csi.NodeUnpublishVolumeResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	op := "NodeUnpublishVolume"
	result := "FAILURE"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)

	logger := log.Ctx(ctx)
	logger.Info().Msg(">>>> Received request")
	defer func() {
		level := zerolog.InfoLevel
		if result != "SUCCESS" {
			level = zerolog.ErrorLevel
		}
		logger.WithLevel(level).Str("result", result).Msg("<<<< Completed processing request")
	}()

	ctx, cancel := context.WithTimeout(ctx, ns.config.grpcRequestTimeout)
	err, dec := ns.acquireSemaphore(ctx, op)
	defer dec()
	defer cancel()
	if err != nil {
		return NodeUnpublishVolumeError(ctx, codes.Unavailable, "Too many concurrent requests, please retry")
	}

	if len(req.GetTargetPath()) == 0 {
		return NodeUnpublishVolumeError(ctx, codes.InvalidArgument, "Target path missing in request")
	}
	targetPath := req.GetTargetPath()

	// TODO: Verify that targetPath is indeed equals to expected source of bind mount
	//		 Which is not straightforward in case plugin was restarted, as in this case
	//		 we lose information of source. Probably Context can be used
	logger.Debug().Str("target_path", targetPath).Msg("Checking if target path exists")
	if _, err := os.Stat(targetPath); err != nil {
		if os.IsNotExist(err) {
			logger.Debug().Msg("Target path does not exist, assuming repeating unpublish request")
			result = "SUCCESS"
			return &csi.NodeUnpublishVolumeResponse{}, nil
		} else if pathErr, ok := err.(*os.PathError); ok && errors.Is(pathErr.Err, syscall.ESTALE) {
			logger.Debug().Msg("Target path is stale, assuming NFS publish failure")
			goto FORCEUMOUNT
		} else {
			logger.Error().Err(err).Msg("Failed to check target path")
			return NodeUnpublishVolumeError(ctx, codes.Internal, "unexpected situation, please contact support")
		}

	}
	// check if this path is a wekafs mount
	if PathIsWekaMount(ctx, targetPath) {
		logger.Debug().Msg("Directory exists and is weka mount")
	} else {
		msg := fmt.Sprintf("Directory %s exists, but not a weka mount, assuming already unpublished", targetPath)
		logger.Warn().Msg(msg)
		if err := os.Remove(targetPath); err != nil {
			result = "FAILURE"
			return NodeUnpublishVolumeError(ctx, codes.Internal, err.Error())
		}
		result = "SUCCESS_WITH_WARNING"
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

FORCEUMOUNT:
	logger.Trace().Str("target_path", targetPath).Msg("Unmounting")
	if err := mount.New("").Unmount(targetPath); err != nil {
		//it seems that when NodeUnpublishRequest appears, this target path is already not existing, e.g. due to pod being deleted
		return NodeUnpublishVolumeError(ctx, codes.Internal, err.Error())
	} else {
		logger.Trace().Msg("Success")
	}
	logger.Trace().Str("target_path", targetPath).Msg("Removing stale target path")
	if err := os.Remove(targetPath); err != nil {
		return NodeUnpublishVolumeError(ctx, codes.Internal, err.Error())
	}

	result = "SUCCESS"
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NodeStageVolume is not supported")
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NodeUnstageVolume is not supported")
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	op := "NodeGetInfo"
	result := "SUCCESS"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)

	logger := log.Ctx(ctx)
	logger.Info().Msg(">>>> Received request")
	defer func() {
		level := zerolog.InfoLevel
		if result != "SUCCESS" {
			level = zerolog.ErrorLevel
		}
		logger.WithLevel(level).Str("result", result).Msg("<<<< Completed processing request")
	}()
	driverName := ns.getConfig().GetDriver().name
	type topologySegments map[string]string
	segments := topologySegments{
		TopologyKeyNode:         ns.nodeID,
		TopologyLabelWekaGlobal: "true", // for backward compatibility remains as is
	}
	if ns.config.manageNodeTopologyLabels {
		// this will either overwrite or add the keys based on the driver name
		segments[fmt.Sprintf(TopologyLabelNodePattern, driverName)] = ns.nodeID
		segments[fmt.Sprintf(TopologyLabelTransportPattern, driverName)] = string(ns.getMounter(ctx).getTransport()) // for backward compatibility, return the preferred transport
		segments[fmt.Sprintf(TopologyLabelWekaLocalPattern, driverName)] = "true"
	} else {
		logger.Warn().Msg("Node topology labels management is disabled, using global label only")
	}

	topology := &csi.Topology{
		Segments: segments,
	}

	return &csi.NodeGetInfoResponse{
		NodeId:             ns.nodeID,
		MaxVolumesPerNode:  ns.maxVolumesPerNode,
		AccessibleTopology: topology,
	}, nil
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: ns.caps,
	}, nil
}

func getNodeServiceCapabilities(nl []csi.NodeServiceCapability_RPC_Type) []*csi.NodeServiceCapability {
	var nsc []*csi.NodeServiceCapability

	for _, capability := range nl {
		log.Info().Str("capability", capability.String()).Msg("Enabling node service capability")
		nsc = append(nsc, &csi.NodeServiceCapability{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: capability,
				},
			},
		})
	}

	return nsc
}

func (ns *NodeServer) GetAcquiredSemaphoreCount(op string) (int64, error) {
	ns.Lock()
	defer ns.Unlock()

	sem, ok := ns.semaphores[op]
	if !ok {
		return 0, fmt.Errorf("semaphore for operation %s not found", op)
	}

	sem.mu.Lock()
	defer sem.mu.Unlock()

	return sem.CurrentCount(), nil
}
