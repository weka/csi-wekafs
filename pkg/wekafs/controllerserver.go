/*
Copyright 2019-2022 Weka.io LTD and The Kubernetes Authors.

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
	"os"
	"strings"
	"sync"
	"time"
)

const (
	deviceID                            = "deviceID"
	maxVolumeIdLength                   = 1920
	TracerName                          = "weka-csi"
	ControlServerAdditionalMountOptions = MountOptionAcl + "," + MountOptionWriteCache
)

type ControllerServer struct {
	csi.UnimplementedControllerServer
	caps       []*csi.ControllerServiceCapability
	nodeID     string
	mounters   *MounterGroup
	api        *ApiStore
	config     *DriverConfig
	semaphores map[string]*SemaphoreWrapper
	metrics    *ControllerServerMetrics
	sync.Mutex
}

func (cs *ControllerServer) getDefaultMountOptions() MountOptions {
	return getDefaultMountOptions().MergedWith(NewMountOptionsFromString(ControlServerAdditionalMountOptions), cs.getConfig().mutuallyExclusiveOptions)
}

func (cs *ControllerServer) getNodeId() string {
	return cs.nodeID
}

func (cs *ControllerServer) getConfig() *DriverConfig {
	return cs.config
}

func (cs *ControllerServer) getMounter(ctx context.Context) AnyMounter {
	return cs.mounters.GetPreferredMounter(ctx)
}

func (cs *ControllerServer) getMounterByTransport(ctx context.Context, transport DataTransport) AnyMounter {
	return cs.mounters.GetMounterByTransport(ctx, transport)
}

func (cs *ControllerServer) getApiStore() *ApiStore {
	return cs.api
}

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) ControllerPublishVolume(c context.Context, request *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	panic("implement me")
}

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) ControllerUnpublishVolume(c context.Context, request *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	panic("implement me")
}

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) ListVolumes(c context.Context, request *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	panic("implement me")
}

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) GetCapacity(c context.Context, request *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	panic("implement me")
}

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) ControllerGetVolume(context.Context, *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	panic("implement me")
}

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) ControllerModifyVolume(context.Context, *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	panic("implement me")
}

func NewControllerServer(driver *WekaFsDriver) *ControllerServer {
	if driver == nil {
		panic("driver is nil")
	}

	exposedCapabilities := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
		csi.ControllerServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER, // add ReadWriteOncePod support
	}
	if driver.config.advertiseSnapshotSupport {
		exposedCapabilities = append(exposedCapabilities, csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT)
	}
	if driver.config.advertiseVolumeCloneSupport {
		exposedCapabilities = append(exposedCapabilities, csi.ControllerServiceCapability_RPC_CLONE_VOLUME)
	}

	capabilities := getControllerServiceCapabilities(exposedCapabilities)

	return &ControllerServer{
		caps:       capabilities,
		nodeID:     driver.nodeID,
		mounters:   driver.mounters,
		api:        driver.api,
		config:     driver.config,
		semaphores: make(map[string]*SemaphoreWrapper),
		metrics:    NewControllerServerMetrics(),
	}
}

func CreateVolumeError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.CreateVolumeResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error creating volume")
	return &csi.CreateVolumeResponse{}, err
}

// CheckCreateVolumeRequestSanity returns error if any generic validation fails
func (cs *ControllerServer) CheckCreateVolumeRequestSanity(ctx context.Context, req *csi.CreateVolumeRequest) error {
	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		log.Ctx(ctx).Err(err).Fields(req).Msg("invalid create volume request")
		return err
	}

	// Check arguments
	if len(req.GetName()) == 0 {
		return status.Error(codes.InvalidArgument, "Name missing in request")
	}
	caps := req.GetVolumeCapabilities()
	if caps == nil {
		return status.Error(codes.InvalidArgument, "Volume Capabilities missing in request")
	}

	// Validate access type in request
	for _, capability := range caps {
		if capability.GetBlock() != nil {
			return status.Error(codes.InvalidArgument, "Block accessType is unsupported")
		}
	}

	// Check no duplicate contentSource specified
	cSource := req.GetVolumeContentSource()
	if cSource != nil && cSource.GetVolume() != nil && cSource.GetSnapshot() != nil {
		return status.Error(codes.InvalidArgument, "Cannot use multiple content sources in CreateVolumeRequest")
	}

	return nil
}

type releaseSemaphore func()

func (cs *ControllerServer) acquireSemaphore(ctx context.Context, op string) (error, releaseSemaphore) {
	logger := log.Ctx(ctx)
	cs.initializeSemaphore(ctx, op)
	sem := cs.semaphores[op]

	logger.Trace().Msg("Acquiring semaphore")
	start := time.Now()
	err := sem.Acquire(ctx, 1)
	elapsed := time.Since(start)

	// select metrics histogram based on the operation type
	var histogram *prometheus.HistogramVec
	var gauge *prometheus.GaugeVec
	driverName := cs.getConfig().GetDriver().name

	switch op {
	case "CreateVolume":
		histogram = cs.metrics.Concurrency.CreateVolumeWaitDuration
		gauge = cs.metrics.Concurrency.CreateVolume
	case "DeleteVolume":
		histogram = cs.metrics.Concurrency.DeleteVolumeWaitDuration
		gauge = cs.metrics.Concurrency.DeleteVolume
	case "ExpandVolume":
		histogram = cs.metrics.Concurrency.ExpandVolumeWaitDuration
		gauge = cs.metrics.Concurrency.ExpandVolume
	case "CreateSnapshot":
		histogram = cs.metrics.Concurrency.CreateSnapshotWaitDuration
		gauge = cs.metrics.Concurrency.CreateSnapshot
	case "DeleteSnapshot":
		histogram = cs.metrics.Concurrency.DeleteSnapshotWaitDuration
		gauge = cs.metrics.Concurrency.DeleteSnapshot
	}

	// update concurrent operations
	currentOps := func() {
		if gauge != nil {
			gauge.WithLabelValues(driverName, "acquired").Set(float64(sem.CurrentCount()))
		}
	}
	currentOps()

	if err == nil {
		if histogram != nil {
			histogram.WithLabelValues(driverName, "success").Observe(elapsed.Seconds())
		}
		logger.Trace().Dur("acquire_duration", elapsed).Str("op", op).Msg("Successfully acquired semaphore")
		return nil, func() {
			defer currentOps()
			elapsed = time.Since(start)
			logger.Trace().Dur("total_operation_time", elapsed).Str("op", op).Msg("Releasing semaphore")
			sem.Release(1)
		}
	}
	logger.Trace().Dur("acquire_duration", elapsed).Str("op", op).Msg("Failed to acquire semaphore")
	if histogram != nil {
		histogram.WithLabelValues(driverName, "failure").Observe(elapsed.Seconds())
	}
	return err, func() {}
}

func (cs *ControllerServer) initializeSemaphore(ctx context.Context, op string) {
	if _, ok := cs.semaphores[op]; ok {
		return
	}
	cs.Lock()
	defer cs.Unlock()

	if _, ok := cs.semaphores[op]; ok {
		return
	}
	m, ok := cs.getConfig().maxConcurrencyPerOp[op]
	if !ok { // if not set, default to 1
		m = 1
	}
	logger := log.Ctx(ctx)
	logger.Info().Str("op", op).Int64("max_concurrency", m).Msg("Initializing semaphore")
	sem := NewSemaphoreWrapper(m)
	cs.semaphores[op] = sem
}

func (cs *ControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	op := "CreateVolume"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	ctx = context.WithValue(ctx, "startTime", time.Now())

	params := req.GetParameters()
	result := "FAILURE"
	logger := log.Ctx(ctx)
	logger.Info().Str("name", req.GetName()).Fields(params).Msg(">>>> Received request")
	var backingType VolumeBackingType
	defer func() {
		level := zerolog.InfoLevel
		if result != "SUCCESS" {
			level = zerolog.ErrorLevel
		}
		driverName := cs.getConfig().GetDriver().name
		bt := string(backingType)
		if bt != "" {
			cs.metrics.Operations.CreateVolumeTotalCapacity.WithLabelValues(driverName, result, bt).Add(float64(req.GetCapacityRange().GetRequiredBytes()))
			cs.metrics.Operations.CreateVolumeCounter.WithLabelValues(driverName, result, bt).Inc()
			cs.metrics.Operations.CreateVolumeDuration.WithLabelValues(driverName, result, bt).Observe(time.Since(ctx.Value("startTime").(time.Time)).Seconds())
		}

		logger.WithLevel(level).Str("result", result).Msg("<<<< Completed processing request")
	}()

	ctx, cancel := context.WithTimeout(ctx, cs.config.grpcRequestTimeout)
	err, dec := cs.acquireSemaphore(ctx, op)
	defer dec()
	defer cancel()
	if err != nil {
		return CreateVolumeError(ctx, codes.Unavailable, "Too many concurrent requests, please retry")
	}

	// First, validate that basic request validation passes
	if err := cs.CheckCreateVolumeRequestSanity(ctx, req); err != nil {
		logger.Error().Err(err).Msg("")
		return nil, err
	}

	// create a logical representation of new volume
	volume, err := NewVolumeFromControllerCreateRequest(ctx, req, cs)
	if err != nil {
		return nil, err
	}
	if volume == nil {
		return CreateVolumeError(ctx, codes.Internal, "Could not initialize volume representation object from request")
	}
	backingType = volume.GetBackingType()

	// check if with current API client state we can modify this volume or not
	if err := volume.CanBeOperated(); err != nil {
		return CreateVolumeError(ctx, codes.InvalidArgument, err.Error())
	}

	// Check for maximum available capacity
	capacity := req.GetCapacityRange().GetRequiredBytes()

	// IDEMPOTENCE FLOW: If directory already exists, return the createResponse if size matches, or error
	volExists, volMatchesCapacity, err := volumeExistsAndMatchesCapacity(ctx, volume, capacity)

	// set params to have all relevant mount options (default + those received in params) to be passed as part of volumeContext
	// omit the container_name though as it should only be set via API secret and not via mount options
	params["mountOptions"] = volume.getMountOptions(ctx).AsVolumeContext()
	params["provisionedByCsiVersion"] = cs.getConfig().GetVersion()

	if err != nil {
		if !volExists {
			return CreateVolumeError(ctx, codes.Internal, fmt.Sprintf("Could not check if volume %s exists: %s", volume.GetId(), err.Error()))
		} else {

			//return CreateVolumeError(ctx, codes.Internal, fmt.Sprintf("Could not check for capacity of existing volume %s: %s", volume.GetId(), err.Error()))
			logger.Error().Msg("Failed to fetch volume capacity, assuming it was not set")
		}
	}

	if volExists && volMatchesCapacity {
		result = "SUCCESS"
		return &csi.CreateVolumeResponse{
			Volume: &csi.Volume{
				VolumeId:           volume.GetId(),
				CapacityBytes:      req.GetCapacityRange().GetRequiredBytes(),
				VolumeContext:      params,
				ContentSource:      volume.getCsiContentSource(ctx),
				AccessibleTopology: cs.generateAccessibleTopology(),
			},
		}, nil
	} else if volExists && err == nil {
		// current capacity explicitly differs from requested, this is another volume request
		return CreateVolumeError(ctx, codes.AlreadyExists, "Volume with same name and different capacity already exists")
	} else if volExists {
		// can happen if volume is half-made (object was created but capacity was not set on it on previous run)
		if err := volume.UpdateCapacity(ctx, &volume.enforceCapacity, capacity); err == nil {
			result = "SUCCESS"
			return &csi.CreateVolumeResponse{
				Volume: &csi.Volume{
					VolumeId:           volume.GetId(),
					CapacityBytes:      req.GetCapacityRange().GetRequiredBytes(),
					VolumeContext:      params,
					ContentSource:      volume.getCsiContentSource(ctx),
					AccessibleTopology: cs.generateAccessibleTopology(),
				},
			}, nil

		} else {
			logger.Error().Err(err).Msg("Failed to fetch OR set capacity for a volume")
			return CreateVolumeError(ctx, codes.Internal, "failed to fetch or set capacity for volume")
		}
	}

	// Actually try to create the volume here
	logger.Info().Int64("capacity", capacity).Str("volume_id", volume.GetId()).Msg("Creating volume")
	if err := volume.Create(ctx, capacity); err != nil {
		return nil, err
	}
	result = "SUCCESS"
	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:           volume.GetId(),
			CapacityBytes:      req.GetCapacityRange().GetRequiredBytes(),
			VolumeContext:      params,
			ContentSource:      volume.getCsiContentSource(ctx),
			AccessibleTopology: cs.generateAccessibleTopology(),
		},
	}, nil
}

func (cs *ControllerServer) generateAccessibleTopology() []*csi.Topology {
	accessibleTopology := make(map[string]string)
	driverName := cs.getConfig().GetDriver().name
	localWekaLabel := fmt.Sprintf(TopologyLabelWekaLocalPattern, driverName)
	accessibleTopology[TopologyLabelWekaGlobal] = "true"
	accessibleTopology[localWekaLabel] = "true"
	return []*csi.Topology{
		{
			Segments: accessibleTopology,
		},
	}
}

func DeleteVolumeError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.DeleteVolumeResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error deleting volume")
	return &csi.DeleteVolumeResponse{}, err
}

func (cs *ControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	op := "DeleteVolume"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	ctx = context.WithValue(ctx, "startTime", time.Now())

	volumeID := req.GetVolumeId()
	logger := log.Ctx(ctx)
	result := "FAILURE"
	logger.Info().Str("volume_id", volumeID).Msg(">>>> Received request")
	capacity := int64(0)
	var backingType VolumeBackingType
	driverName := cs.getConfig().GetDriver().name
	defer func() {
		level := zerolog.InfoLevel
		if result != "SUCCESS" {
			level = zerolog.ErrorLevel
		}
		bt := string(backingType)
		if bt != "" {
			if capacity > 0 {
				cs.metrics.Operations.DeleteVolumeTotalCapacity.WithLabelValues(driverName, result, bt).Add(float64(capacity))
			}
			cs.metrics.Operations.DeleteVolumeCounter.WithLabelValues(driverName, result, bt).Inc()
			cs.metrics.Operations.DeleteVolumeDuration.WithLabelValues(driverName, result, bt).Observe(time.Since(ctx.Value("startTime").(time.Time)).Seconds())
		}
		logger.WithLevel(level).Str("result", result).Msg("<<<< Completed processing request")
	}()

	ctx, cancel := context.WithTimeout(ctx, cs.config.grpcRequestTimeout)
	err, dec := cs.acquireSemaphore(ctx, op)
	defer dec()
	defer cancel()
	if err != nil {
		return DeleteVolumeError(ctx, codes.Unavailable, "Too many concurrent requests, please retry")
	}

	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	client, err := cs.api.GetClientFromSecrets(ctx, req.Secrets)
	if err != nil {
		return DeleteVolumeError(ctx, codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}

	volume, err := NewVolumeFromId(ctx, volumeID, client, cs)
	if err != nil {
		// Should return ok on incorrect ID (by CSI spec)
		result = "SUCCESS"
		return &csi.DeleteVolumeResponse{}, nil
	}

	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		logger.Warn().Err(err).Msg("invalid delete volume request")
		return DeleteVolumeError(ctx, codes.Internal, err.Error())
	}

	// obtain capacity and backing for metrics
	backingType = volume.GetBackingType()
	capacity, err = volume.GetCapacity(ctx)
	if err != nil || capacity <= 0 {
		logger.Warn().Err(err).Msg("Failed to fetch volume capacity, assuming it does not exist")
	}

	err = volume.Trash(ctx)
	if os.IsNotExist(err) {
		logger.Debug().Str("volume_id", volume.GetId()).Msg("Volume not found, but returning success for idempotence")
		result = "SUCCESS"
		return &csi.DeleteVolumeResponse{}, nil
	}
	// cleanup
	if err != nil {
		if errors.Is(err, ErrFilesystemHasUnderlyingSnapshots) {
			return &csi.DeleteVolumeResponse{}, err
		}
		return DeleteVolumeError(ctx, codes.Internal, err.Error())
	}
	result = "SUCCESS"
	return &csi.DeleteVolumeResponse{}, nil
}

func ExpandVolumeError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.ControllerExpandVolumeResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error expanding volume")
	return &csi.ControllerExpandVolumeResponse{}, err
}

func (cs *ControllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	op := "ExpandVolume"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	volumeID := req.GetVolumeId()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).
		Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).
		Str("volume_id", volumeID).Logger().WithContext(ctx)
	ctx = context.WithValue(ctx, "startTime", time.Now())

	logger := log.Ctx(ctx)
	result := "FAILURE"
	capacity := int64(-1)
	capRange := req.GetCapacityRange()
	if capRange != nil {
		capacity = capRange.GetRequiredBytes()
	}
	logger.Info().Int64("capacity", capacity).Msg(">>>> Received request")
	var backingType VolumeBackingType
	driverName := cs.getConfig().GetDriver().name
	defer func() {

		level := zerolog.InfoLevel
		if result != "SUCCESS" {
			level = zerolog.ErrorLevel
		}
		bt := string(backingType)
		if bt != "" {
			if capacity > 0 {
				cs.metrics.Operations.ExpandVolumeTotalCapacity.WithLabelValues(driverName, result, bt).Add(float64(capacity))
			}
			cs.metrics.Operations.ExpandVolumeCounter.WithLabelValues(driverName, result, bt).Inc()
			cs.metrics.Operations.ExpandVolumeDuration.WithLabelValues(driverName, result, bt).Observe(time.Since(ctx.Value("startTime").(time.Time)).Seconds())
		}
		logger.WithLevel(level).Str("result", result).Msg("<<<< Completed processing request")
	}()

	ctx, cancel := context.WithTimeout(ctx, cs.config.grpcRequestTimeout)
	err, dec := cs.acquireSemaphore(ctx, op)
	defer dec()
	defer cancel()
	if err != nil {
		return ExpandVolumeError(ctx, codes.Unavailable, "Too many concurrent requests, please retry")
	}

	if capacity == -1 {
		return ExpandVolumeError(ctx, codes.InvalidArgument, "Capacity range not provided")
	}

	if len(req.GetVolumeId()) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "Volume ID not specified")
	}
	client, err := cs.api.GetClientFromSecrets(ctx, req.Secrets)

	if err != nil {
		// this case can happen only if we had client that failed to initialise, and not if we do not have a client at all
		return ExpandVolumeError(ctx, codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}

	volume, err := NewVolumeFromId(ctx, req.GetVolumeId(), client, cs)
	if err != nil {
		return ExpandVolumeError(ctx, codes.NotFound, fmt.Sprintf("Volume with id %s does not exist", req.GetVolumeId()))
	}
	backingType = volume.GetBackingType()

	maxStorageCapacity, err := volume.getMaxCapacity(ctx)
	if err != nil {
		return ExpandVolumeError(ctx, codes.Unknown, fmt.Sprintf("ExpandVolume: Cannot obtain free capacity for volume %s", volume.GetId()))
	}
	if capacity > maxStorageCapacity {
		return ExpandVolumeError(ctx, codes.OutOfRange, fmt.Sprintf("Requested capacity %d exceeds maximum allowed %d", capacity, maxStorageCapacity))
	}

	ok, err := volume.Exists(ctx)
	if err != nil {
		return ExpandVolumeError(ctx, codes.NotFound, err.Error())
	}
	if !ok {
		return ExpandVolumeError(ctx, codes.Internal, "Volume does not exist")
	}

	currentSize, err := volume.GetCapacity(ctx)
	if err != nil {
		return ExpandVolumeError(ctx, codes.Internal, fmt.Sprintf("Could not get volume capacity: %s", err.Error()))
	}
	logger.Debug().Int64("current_capacity", currentSize).Int64("new_capacity", capacity).Msg("Expanding volume capacity")

	if currentSize != capacity {
		if err := volume.UpdateCapacity(ctx, nil, capacity); err != nil {
			return ExpandVolumeError(ctx, codes.Internal, fmt.Sprintf("Could not update volume: %s", err.Error()))
		}
	}
	result = "SUCCESS"
	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         capacity,
		NodeExpansionRequired: false, // since this is filesystem, no need to resize on node
	}, nil
}

func CreateSnapshotError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.CreateSnapshotResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error creating snapshot")
	return &csi.CreateSnapshotResponse{}, err
}

func (cs *ControllerServer) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	op := "CreateSnapshot"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	ctx = context.WithValue(ctx, "startTime", time.Now())

	srcVolumeId := req.GetSourceVolumeId()
	secrets := req.GetSecrets()
	snapName := req.GetName()
	logger := log.Ctx(ctx)
	result := "FAILURE"
	logger.Info().Str("src_volume_id", srcVolumeId).Str("name", snapName).Msg(">>>> Received request")
	defer func() {
		level := zerolog.InfoLevel
		if result != "SUCCESS" {
			level = zerolog.ErrorLevel
		}
		dn := cs.getConfig().GetDriver().name
		cs.metrics.Operations.CreateSnapshotCounter.WithLabelValues(dn, result).Inc()
		cs.metrics.Operations.CreateSnapshotDuration.WithLabelValues(dn, result).Observe(time.Since(ctx.Value("startTime").(time.Time)).Seconds())

		logger.WithLevel(level).Str("result", result).Msg("<<<< Completed processing request")
	}()

	ctx, cancel := context.WithTimeout(ctx, cs.config.grpcRequestTimeout)
	err, dec := cs.acquireSemaphore(ctx, op)
	defer dec()
	defer cancel()
	if err != nil {
		return CreateSnapshotError(ctx, codes.Unavailable, "Too many concurrent requests, please retry")
	}

	if srcVolumeId == "" {
		return CreateSnapshotError(ctx, codes.InvalidArgument, "Cannot create snapshot without specifying SourceVolumeId")
	}

	if snapName == "" {
		return CreateSnapshotError(ctx, codes.InvalidArgument, "Cannot create snapshot without snapName")
	}

	client, err := cs.api.GetClientFromSecrets(ctx, secrets)
	if err != nil {
		return CreateSnapshotError(ctx, codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the req", err))
	}

	srcVolume, err := NewVolumeFromId(ctx, srcVolumeId, client, cs)
	if err != nil {
		return CreateSnapshotError(ctx, codes.InvalidArgument, fmt.Sprintln("Invalid sourceVolumeId", srcVolumeId))
	}

	srcVolExists, err := srcVolume.Exists(ctx)
	if err != nil {
		return CreateSnapshotError(ctx, codes.Internal, fmt.Sprintf("Failed to check for existence of source volume %s", srcVolume.GetId()))
	}
	if !srcVolExists {
		return CreateSnapshotError(ctx, codes.FailedPrecondition, fmt.Sprintf("Could not find source volume %s", srcVolume.GetId()))
	}

	s, err := srcVolume.CreateSnapshot(ctx, snapName)
	if err != nil {
		return &csi.CreateSnapshotResponse{}, err

	}

	ret := &csi.CreateSnapshotResponse{
		Snapshot: s.getCsiSnapshot(ctx),
	}
	result = "SUCCESS"
	return ret, nil
}

func DeleteSnapshotError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.DeleteSnapshotResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error deleting snapshot")
	return &csi.DeleteSnapshotResponse{}, err
}

func (cs *ControllerServer) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	op := "DeleteSnapshot"
	snapshotID := req.GetSnapshotId()
	secrets := req.GetSecrets()
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	ctx = context.WithValue(ctx, "startTime", time.Now())

	logger := log.Ctx(ctx)
	result := "FAILURE"
	logger.Info().Str("snapshot_id", snapshotID).Msg(">>>> Received request")
	defer func() {
		level := zerolog.InfoLevel
		if result != "SUCCESS" {
			level = zerolog.ErrorLevel
		}
		dn := cs.getConfig().GetDriver().name
		cs.metrics.Operations.DeleteSnapshotCounter.WithLabelValues(dn, result).Inc()
		cs.metrics.Operations.DeleteSnapshotDuration.WithLabelValues(dn, result).Observe(time.Since(ctx.Value("startTime").(time.Time)).Seconds())
		logger.WithLevel(level).Str("result", result).Msg("<<<< Completed processing request")
	}()

	ctx, cancel := context.WithTimeout(ctx, cs.config.grpcRequestTimeout)
	err, dec := cs.acquireSemaphore(ctx, op)
	defer dec()
	defer cancel()
	if err != nil {
		return DeleteSnapshotError(ctx, codes.Unavailable, "Too many concurrent requests, please retry")
	}

	if snapshotID == "" {
		return DeleteSnapshotError(ctx, codes.InvalidArgument, "Failed to delete snapshot, no ID specified")
	}
	err = validateSnapshotId(snapshotID)
	if err != nil {
		//according to CSI specs must return OK on invalid ID
		result = "SUCCESS"
		return &csi.DeleteSnapshotResponse{}, nil
	}

	client, err := cs.api.GetClientFromSecrets(ctx, secrets)
	if err != nil {
		return DeleteSnapshotError(ctx, codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the req", err))
	}
	existingSnap, err := NewSnapshotFromId(ctx, snapshotID, client, cs)
	if err != nil {
		return DeleteSnapshotError(ctx, codes.Internal, fmt.Sprintln("Failed to initialize snapshot from ID", snapshotID, err.Error()))
	}
	err = existingSnap.Delete(ctx)
	if err != nil {
		return DeleteSnapshotError(ctx, codes.Internal, fmt.Sprintln("Failed to delete snapshot", snapshotID, err))
	}
	result = "SUCCESS"
	return &csi.DeleteSnapshotResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	panic("Implement me")
}

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	op := "ControllerGetCapabilities"
	result := "SUCCESS"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)

	logger := log.Ctx(ctx)
	logger.Trace().Msg(">>>> Received request")
	defer func() {
		level := zerolog.TraceLevel
		if result != "SUCCESS" {
			level = zerolog.ErrorLevel
		}
		logger.WithLevel(level).Str("result", result).Msg("<<<< Completed processing request")
	}()
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: cs.caps,
	}, nil
}

func ValidateVolumeCapsError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error validating volume capabilities")
	return &csi.ValidateVolumeCapabilitiesResponse{}, err
}

func (cs *ControllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	op := "ValidateVolumeCapabilities"
	volumeID := req.GetVolumeId()
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)

	result := "FAILURE"
	logger := log.Ctx(ctx)
	logger.Info().Str("volume_id", volumeID).Msg(">>>> Received request")
	defer func() {
		level := zerolog.InfoLevel
		if result != "SUCCESS" {
			level = zerolog.ErrorLevel
		}
		logger.WithLevel(level).Str("result", result).Msg("<<<< Completed processing request")
	}()

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return ValidateVolumeCapsError(ctx, codes.InvalidArgument, "Volume ID cannot be empty")
	}
	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, req.GetVolumeId())
	}

	client, err := cs.api.GetClientFromSecrets(ctx, req.Secrets)
	if err != nil {
		return ValidateVolumeCapsError(ctx, codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}

	volume, err := NewVolumeFromId(ctx, req.GetVolumeId(), client, cs)

	if err != nil {
		return ValidateVolumeCapsError(ctx, codes.NotFound, err.Error())
	}
	ok, err := volume.Exists(ctx)
	if err != nil || !ok {
		return ValidateVolumeCapsError(ctx, codes.NotFound, fmt.Sprintf("Could not find volume %s", req.GetVolumeId()))
	}

	for _, capability := range req.GetVolumeCapabilities() {
		if capability.GetMount() == nil && capability.GetBlock() == nil {
			return ValidateVolumeCapsError(ctx, codes.InvalidArgument, "cannot have both Mount and block access type be undefined")
		}
	}
	result = "SUCCESS"
	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeContext:      req.GetVolumeContext(),
			VolumeCapabilities: req.GetVolumeCapabilities(),
			Parameters:         req.GetParameters(),
		},
	}, nil
}

func (cs *ControllerServer) validateControllerServiceRequest(c csi.ControllerServiceCapability_RPC_Type) error {
	if c == csi.ControllerServiceCapability_RPC_UNKNOWN {
		return nil
	}

	for _, capability := range cs.caps {
		if c == capability.GetRpc().GetType() {
			return nil
		}
	}
	return status.Errorf(codes.InvalidArgument, "unsupported capability %s", c)
}

func getControllerServiceCapabilities(cl []csi.ControllerServiceCapability_RPC_Type) []*csi.ControllerServiceCapability {
	var csc []*csi.ControllerServiceCapability

	for _, capability := range cl {
		log.Info().Str("capability", capability.String()).Msg("Enabling ControllerServiceCapability")
		csc = append(csc, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: capability,
				},
			},
		})
	}

	return csc
}
