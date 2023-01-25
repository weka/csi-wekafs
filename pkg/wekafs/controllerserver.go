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
	"bytes"
	"context"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/rs/xid"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
	"strings"
)

const (
	deviceID          = "deviceID"
	maxVolumeIdLength = 1920
)

type ControllerServer struct {
	caps                 []*csi.ControllerServiceCapability
	nodeID               string
	mounter              *wekaMounter
	dynamicVolPath       string
	api                  *ApiStore
	newVolumePrefix      string
	newSnapshotPrefix    string
	allowAutoFsCreation  bool
	allowAutoFsExpansion bool
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

func NewControllerServer(nodeID string, api *ApiStore, mounter *wekaMounter, dynamicVolPath string,
	newVolumePrefix, newSnapshotPrefix string, allowAutoFsCreation, allowAutoFsExpansion,
	supportSnapshotCapability, supportVolumeCloneCapability bool) *ControllerServer {
	exposedCapabilities := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
	}
	if supportSnapshotCapability {
		exposedCapabilities = append(exposedCapabilities, csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT)
	}
	if supportVolumeCloneCapability {
		exposedCapabilities = append(exposedCapabilities, csi.ControllerServiceCapability_RPC_CLONE_VOLUME)
	}

	return &ControllerServer{
		caps:                 getControllerServiceCapabilities(exposedCapabilities),
		nodeID:               nodeID,
		mounter:              mounter,
		dynamicVolPath:       dynamicVolPath,
		api:                  api,
		newVolumePrefix:      newVolumePrefix,
		newSnapshotPrefix:    newSnapshotPrefix,
		allowAutoFsCreation:  allowAutoFsCreation,
		allowAutoFsExpansion: allowAutoFsExpansion,
	}
}

func renderKeyValuePairs(m map[string]string) string {
	b := new(bytes.Buffer)
	for key, value := range m {
		_, _ = fmt.Fprintf(b, "%s=\"%s\"\n", key, value)
	}
	return b.String()
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

func (cs *ControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	ctx = log.With().Str("trace_id", xid.New().String()).Str("op", "CreateVolume").Logger().WithContext(ctx)
	params := req.GetParameters()
	logger := log.Ctx(ctx)
	logger.Info().Str("name", req.GetName()).Fields(params).Msg(">>>> Received request")
	defer logger.Info().Msg("<<<< Completed processing request")

	// First, validate that basic request validation passes
	if err := cs.CheckCreateVolumeRequestSanity(ctx, req); err != nil {
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

	// check if with current API client state we can modify this volume or not
	// (basically only legacy dirVolume with xAttr fallback can be operated without API client)
	if err := volume.canBeOperated(); err != nil {
		return CreateVolumeError(ctx, codes.InvalidArgument, err.Error())
	}

	// Check for maximum available capacity
	capacity := req.GetCapacityRange().GetRequiredBytes()

	// IDEMPOTENCE FLOW: If directory already exists, return the createResponse if size matches, or error
	volExists, volMatchesCapacity, err := volume.ExistsAndMatchesCapacity(ctx, capacity)

	if err != nil {
		if !volExists {
			return CreateVolumeError(ctx, codes.Internal, fmt.Sprintf("Could not check if volume %s exists: %s", volume.GetId(), err.Error()))
		} else {
			return CreateVolumeError(ctx, codes.Internal, fmt.Sprintf("Could not check for capacity of existing volume %s: %s", volume.GetId(), err.Error()))
		}
	}
	if volExists && volMatchesCapacity {
		return &csi.CreateVolumeResponse{
			Volume: &csi.Volume{
				VolumeId:      volume.GetId(),
				CapacityBytes: req.GetCapacityRange().GetRequiredBytes(),
				VolumeContext: params,
			},
		}, nil
	} else if volExists {
		// current capacity differs from requested, this is another volume request
		return CreateVolumeError(ctx, codes.AlreadyExists, fmt.Sprintf("Volume %s already exists, but has different capacity", volume.GetId()))
	}

	// Actually try to create the volume here
	logger.Info().Int64("capacity", capacity).Str("volume_id", volume.GetId()).Msg("Creating volume")
	if err := volume.Create(ctx, capacity); err != nil {
		return nil, err
	}

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volume.GetId(),
			CapacityBytes: req.GetCapacityRange().GetRequiredBytes(),
			VolumeContext: params,
		},
	}, nil
}

func DeleteVolumeError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.DeleteVolumeResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error deleting volume")
	return &csi.DeleteVolumeResponse{}, err
}

func (cs *ControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	ctx = log.With().Str("trace_id", xid.New().String()).Str("op", "DeleteVolume").Logger().WithContext(ctx)
	volumeID := req.GetVolumeId()
	logger := log.Ctx(ctx)
	logger.Info().Str("volume_id", volumeID).Msg(">>>> Received request")
	defer logger.Info().Msg("<<<< Completed processing request")
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	client, err := cs.api.GetClientFromSecrets(ctx, req.Secrets)
	if err != nil {
		return DeleteVolumeError(ctx, codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}

	volume, err := NewVolumeFromId(ctx, volumeID, client, cs.mounter)
	if err != nil {
		// Should return ok on incorrect ID (by CSI spec)
		return &csi.DeleteVolumeResponse{}, nil
	}

	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		logger.Warn().Err(err).Msg("invalid delete volume request")
		return DeleteVolumeError(ctx, codes.Internal, err.Error())
	}

	err = volume.moveToTrash(ctx)
	if os.IsNotExist(err) {
		logger.Debug().Str("volume_id", volume.GetId()).Msg("Volume not found, but returning success for idempotence")
		return &csi.DeleteVolumeResponse{}, nil
	}
	// cleanup
	if err != nil {
		return DeleteVolumeError(ctx, codes.Internal, err.Error())
	}
	return &csi.DeleteVolumeResponse{}, nil
}

func ExpandVolumeError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.ControllerExpandVolumeResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error expanding volume")
	return &csi.ControllerExpandVolumeResponse{}, err
}

func (cs *ControllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	ctx = log.With().Str("trace_id", xid.New().String()).Str("op", "ExpandVolume").Logger().WithContext(ctx)
	volumeID := req.GetVolumeId()
	logger := log.Ctx(ctx)
	logger.Info().Str("volume_id", volumeID).Str("capacity", req.GetCapacityRange().String()).Msg(">>>> Received request")
	defer logger.Info().Msg("<<<< Completed processing request")

	if len(req.GetVolumeId()) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "Volume ID not specified")
	}
	client, err := cs.api.GetClientFromSecrets(ctx, req.Secrets)

	if err != nil {
		// this case can happen only if we had client that failed to initialise, and not if we do not have a client at all
		return ExpandVolumeError(ctx, codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}

	volume, err := NewVolumeFromId(ctx, req.GetVolumeId(), client, cs.mounter)
	if err != nil {
		return ExpandVolumeError(ctx, codes.NotFound, fmt.Sprintf("Volume with id %s does not exist", req.GetVolumeId()))
	}

	capRange := req.GetCapacityRange()
	if capRange == nil {
		return ExpandVolumeError(ctx, codes.InvalidArgument, "Capacity range not provided")
	}

	capacity := capRange.GetRequiredBytes()

	maxStorageCapacity, err := volume.getMaxCapacity(ctx)
	if err != nil {
		return ExpandVolumeError(ctx, codes.Unknown, fmt.Sprintf("ExpandVolume: Cannot obtain free capacity for volume %s", volume.GetId()))
	}
	if capacity > maxStorageCapacity {
		return ExpandVolumeError(ctx, codes.OutOfRange, fmt.Sprintf("Requested capacity %d exceeds maximum allowed %d", capacity, maxStorageCapacity))
	}

	ok, err := volume.Exists(ctx)
	if err != nil {
		return ExpandVolumeError(ctx, codes.Internal, err.Error())
	}
	if !ok {
		return ExpandVolumeError(ctx, codes.Internal, "Volume does not exist")
	}

	currentSize, err := volume.GetCapacity(ctx)
	if err != nil {
		return ExpandVolumeError(ctx, codes.Internal, "Could not get volume capacity")
	}
	logger.Debug().Int64("current_capacity", currentSize).Int64("new_capacity", capacity).Msg("Expanding volume capacity")

	if currentSize != capacity {
		if err := volume.UpdateCapacity(ctx, nil, capacity); err != nil {
			return ExpandVolumeError(ctx, codes.Internal, fmt.Sprintf("Could not update volume %s: %v", volume, err))
		}
	}
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

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	ctx = log.With().Str("trace_id", xid.New().String()).Str("op", "CreateSnapshot").Logger().WithContext(ctx)
	srcVolumeId := req.GetSourceVolumeId()
	secrets := req.GetSecrets()
	snapName := req.GetName()
	logger := log.Ctx(ctx)
	logger.Info().Str("src_volume_id", srcVolumeId).Str("name", snapName).Msg(">>>> Received request")
	defer logger.Info().Msg("<<<< Completed processing request")

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

	srcVolume, err := NewVolumeFromId(ctx, srcVolumeId, client, cs.mounter)
	if err != nil {
		return CreateSnapshotError(ctx, codes.InvalidArgument, fmt.Sprintln("Invalid sourceVolumeId", srcVolumeId))
	}

	srcVolExists, err := srcVolume.Exists(ctx)
	if err != nil {
		return CreateSnapshotError(ctx, codes.Internal, fmt.Sprintln("Failed to check for existence of source volume", srcVolume.String()))
	}
	if !srcVolExists {
		return CreateSnapshotError(ctx, codes.FailedPrecondition, fmt.Sprintln("Could not find source colume", srcVolume.String()))
	}

	s, err := srcVolume.CreateSnapshot(ctx, snapName)
	if err != nil {
		return CreateSnapshotError(ctx, codes.Internal, fmt.Sprintln("Failed to create snapshot for volume", err.Error()))
	}

	ret := &csi.CreateSnapshotResponse{
		Snapshot: s.getCsiSnapshot(ctx),
	}
	return ret, nil
}

func DeleteSnapshotError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.DeleteSnapshotResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error deleting snapshot")
	return &csi.DeleteSnapshotResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	snapshotID := req.GetSnapshotId()
	secrets := req.GetSecrets()
	ctx = log.With().Str("trace_id", xid.New().String()).Str("op", "DeleteSnapshot").Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	logger.Info().Str("snapshot_id", snapshotID).Msg(">>>> Received request")
	defer logger.Info().Msg("<<<< Completed processing request")
	if snapshotID == "" {
		return DeleteSnapshotError(ctx, codes.InvalidArgument, "Failed to delete snapshot, no ID specified")
	}
	err := validateSnapshotId(snapshotID)
	if err != nil {
		//according to CSI specs must return OK on invalid ID
		return &csi.DeleteSnapshotResponse{}, nil
	}

	client, err := cs.api.GetClientFromSecrets(ctx, secrets)
	if err != nil {
		return DeleteSnapshotError(ctx, codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the req", err))
	}
	existingSnap, err := NewSnapshotFromId(ctx, snapshotID, client)
	if err != nil {
		return DeleteSnapshotError(ctx, codes.Internal, fmt.Sprintln("Failed to initialize snapshot from ID", snapshotID, err.Error()))
	}
	err = existingSnap.Delete(ctx)
	if err != nil {
		return DeleteSnapshotError(ctx, codes.Internal, fmt.Sprintln("Failed to delete snapshot", snapshotID, err))
	}
	return &csi.DeleteSnapshotResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) ListSnapshots(c context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	panic("Implement me")
}

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: cs.caps,
	}, nil
}

func ValidateVolumeCapsError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error validating volume capabilities")
	return &csi.ValidateVolumeCapabilitiesResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	ctx = log.With().Str("trace_id", xid.New().String()).Str("op", "ValidateVolumeCapabilities").Logger().WithContext(ctx)
	volumeID := req.GetVolumeId()
	logger := log.Ctx(ctx)
	logger.Info().Str("volume_id", volumeID).Msg(">>>> Received request")
	defer logger.Info().Msg("<<<< Completed processing request")

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

	volume, err := NewVolumeFromId(ctx, req.GetVolumeId(), client, cs.mounter)
	if err != nil {
		return ValidateVolumeCapsError(ctx, codes.Internal, err.Error())
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
