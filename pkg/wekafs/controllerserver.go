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
	"github.com/golang/glog"
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

func CreateVolumeError(errorCode codes.Code, errorMessage string) (*csi.CreateVolumeResponse, error) {
	glog.ErrorDepth(1, fmt.Sprintln("Error creating volume, code:", errorCode, ", error:", errorMessage))
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	return &csi.CreateVolumeResponse{}, err
}

// CheckCreateVolumeRequestSanity returns error if any generic validation fails
func (cs *ControllerServer) CheckCreateVolumeRequestSanity(ctx context.Context, req *csi.CreateVolumeRequest) error {
	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		glog.V(3).Infof("invalid create volume req: %v", req)
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
	params := req.GetParameters()
	renderedParams := renderKeyValuePairs(params)
	glog.V(3).Infof(">>>> Received a CreateVolume request: %s (%s)", req.GetName(), renderedParams)
	defer glog.V(3).Infof("<<<< Completed processing CreateVolume request: %s (%s)", req.GetName(), renderedParams)

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
		return CreateVolumeError(codes.Internal, "Could not initialize volume representation object from request")
	}

	// check if with current API client state we can modify this volume or not
	// (basically only legacy dirVolume with xAttr fallback can be operated without API client)
	if err := volume.canBeOperated(); err != nil {
		return CreateVolumeError(codes.InvalidArgument, err.Error())
	}

	// Check for maximum available capacity
	capacity := req.GetCapacityRange().GetRequiredBytes()

	// IDEMPOTENCE FLOW: If directory already exists, return the createResponse if size matches, or error
	volExists, volMatchesCapacity, err := volume.ExistsAndMatchesCapacity(ctx, capacity)

	if err != nil {
		if !volExists {
			return CreateVolumeError(codes.Internal, fmt.Sprintf("Could not check if volume %s exists: %s", volume.GetId(), err.Error()))
		} else {
			return CreateVolumeError(codes.Internal, fmt.Sprintf("Could not check for capacity of existing volume %s: %s", volume.GetId(), err.Error()))
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
		return CreateVolumeError(codes.AlreadyExists, fmt.Sprintf("Volume %s already exists, but has different capacity", volume.GetId()))
	}

	// Actually try to create the volume here
	glog.V(3).Infoln("Creating volume", volume.GetId(), "capacity:", capacity)
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

func DeleteVolumeError(errorCode codes.Code, errorMessage string) (*csi.DeleteVolumeResponse, error) {
	glog.ErrorDepth(1, fmt.Sprintln("Error deleting volume, code:", errorCode, ", error:", errorMessage))
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	return &csi.DeleteVolumeResponse{}, err
}

func (cs *ControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	glog.V(3).Infof(">>>> Received a DeleteVolume request for volume ID %s", req.GetVolumeId())
	defer glog.V(3).Infof("<<<< Completed processing DeleteVolume request for volume ID %s", req.GetVolumeId())
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	client, err := cs.api.GetClientFromSecrets(ctx, req.Secrets)
	if err != nil {
		return DeleteVolumeError(codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}

	volume, err := NewVolumeFromId(ctx, volumeID, client, cs.mounter)
	if err != nil {
		// Should return ok on incorrect ID (by CSI spec)
		return &csi.DeleteVolumeResponse{}, nil
	}

	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		glog.V(3).Infof("invalid delete volume req: %v", req)
		return DeleteVolumeError(codes.Internal, err.Error())
	}

	err = volume.moveToTrash(ctx)
	if os.IsNotExist(err) {
		glog.V(4).Infof("Volume not found %s, but returning success for idempotence", volume.GetId())
		return &csi.DeleteVolumeResponse{}, nil
	}
	// cleanup
	if err != nil {
		return DeleteVolumeError(codes.Internal, err.Error())
	}
	return &csi.DeleteVolumeResponse{}, nil
}

func ExpandVolumeError(errorCode codes.Code, errorMessage string) (*csi.ControllerExpandVolumeResponse, error) {
	glog.ErrorDepth(1, fmt.Sprintln("Error expanding volume, code:", errorCode, ", error:", errorMessage))
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	return &csi.ControllerExpandVolumeResponse{}, err
}

func (cs *ControllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	glog.V(3).Infof(">>>> Received a ControllerExpandVolume request for volume ID %s", req.GetVolumeId())
	defer glog.V(3).Infof("<<<< Completed processing ControllerExpandVolume request for volume ID %s", req.GetVolumeId())

	if len(req.GetVolumeId()) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "Volume ID not specified")
	}
	client, err := cs.api.GetClientFromSecrets(ctx, req.Secrets)

	if err != nil {
		// this case can happen only if we had client that failed to initialise, and not if we do not have a client at all
		return ExpandVolumeError(codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}

	volume, err := NewVolumeFromId(ctx, req.GetVolumeId(), client, cs.mounter)
	if err != nil {
		return ExpandVolumeError(codes.NotFound, fmt.Sprintf("Volume with id %s does not exist", req.GetVolumeId()))
	}

	capRange := req.GetCapacityRange()
	if capRange == nil {
		return ExpandVolumeError(codes.InvalidArgument, "Capacity range not provided")
	}

	capacity := capRange.GetRequiredBytes()

	maxStorageCapacity, err := volume.getMaxCapacity(ctx)
	if err != nil {
		return ExpandVolumeError(codes.Unknown, fmt.Sprintf("ExpandVolume: Cannot obtain free capacity for volume %s", volume.GetId()))
	}
	if capacity > maxStorageCapacity {
		return ExpandVolumeError(codes.OutOfRange, fmt.Sprintf("Requested capacity %d exceeds maximum allowed %d", capacity, maxStorageCapacity))
	}

	ok, err := volume.Exists(ctx)
	if err != nil {
		return ExpandVolumeError(codes.Internal, err.Error())
	}
	if !ok {
		return ExpandVolumeError(codes.Internal, "Volume does not exist")
	}

	currentSize, err := volume.GetCapacity(ctx)
	if err != nil {
		return ExpandVolumeError(codes.Internal, "Could not get volume capacity")
	}
	glog.Infof("Volume %s: current capacity: %d, expanding to %d", volume.GetId(), currentSize, capacity)

	if currentSize != capacity {
		if err := volume.UpdateCapacity(ctx, nil, capacity); err != nil {
			return ExpandVolumeError(codes.Internal, fmt.Sprintf("Could not update volume %s: %v", volume, err))
		}
	}
	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         capacity,
		NodeExpansionRequired: false, // since this is filesystem, no need to resize on node
	}, nil
}

func CreateSnapshotError(errorCode codes.Code, errorMessage string) (*csi.CreateSnapshotResponse, error) {
	glog.ErrorDepth(1, fmt.Sprintln("Error creating snapshot, code:", errorCode, ", error:", errorMessage))
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	return &csi.CreateSnapshotResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) CreateSnapshot(ctx context.Context, request *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	srcVolumeId := request.GetSourceVolumeId()
	secrets := request.GetSecrets()
	snapName := request.GetName()
	glog.V(3).Infof(">>>> Received a CreateSnapshot request for srcVolume %s, snapName: %s", srcVolumeId, snapName)
	defer glog.V(3).Infof("<<<< Completed processing CreateSnapshot request for srcVolume %s, snapName: %s", srcVolumeId, snapName)

	if srcVolumeId == "" {
		return CreateSnapshotError(codes.InvalidArgument, "Cannot create snapshot without specifying SourceVolumeId")
	}

	if snapName == "" {
		return CreateSnapshotError(codes.InvalidArgument, "Cannot create snapshot without snapName")
	}

	client, err := cs.api.GetClientFromSecrets(ctx, secrets)
	if err != nil {
		return CreateSnapshotError(codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}

	srcVolume, err := NewVolumeFromId(ctx, srcVolumeId, client, cs.mounter)
	if err != nil {
		return CreateSnapshotError(codes.InvalidArgument, fmt.Sprintln("Invalid sourceVolumeId", srcVolumeId))
	}

	srcVolExists, err := srcVolume.Exists(ctx)
	if err != nil {
		return CreateSnapshotError(codes.Internal, fmt.Sprintln("Failed to check for existence of source volume", srcVolume.String()))
	}
	if !srcVolExists {
		return CreateSnapshotError(codes.FailedPrecondition, fmt.Sprintln("Could not find source colume", srcVolume.String()))
	}

	s, err := srcVolume.CreateSnapshot(ctx, snapName)
	if err != nil {
		return CreateSnapshotError(codes.Internal, fmt.Sprintln("Failed to create snapshot for volume", err.Error()))
	}

	ret := &csi.CreateSnapshotResponse{
		Snapshot: s.getCsiSnapshot(ctx),
	}
	return ret, nil
}

func DeleteSnapshotError(errorCode codes.Code, errorMessage string) (*csi.DeleteSnapshotResponse, error) {
	glog.ErrorDepth(1, fmt.Sprintln("Error deleting snapshot, code:", errorCode, ", error:", errorMessage))
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	return &csi.DeleteSnapshotResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) DeleteSnapshot(ctx context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	snapshotID := request.GetSnapshotId()
	secrets := request.GetSecrets()
	glog.V(3).Infoln(">>>> Received a DeleteSnapshot request for snapshotId", snapshotID)
	defer glog.V(3).Infoln("<<<< Completed processing DeleteSnapshot request for snapshotId", snapshotID)
	if snapshotID == "" {
		return DeleteSnapshotError(codes.InvalidArgument, "Failed to delete snapshot, no ID specified")
	}
	err := validateSnapshotId(snapshotID)
	if err != nil {
		//according to CSI specs must return OK on invalid ID
		return &csi.DeleteSnapshotResponse{}, nil
	}

	client, err := cs.api.GetClientFromSecrets(ctx, secrets)
	if err != nil {
		return DeleteSnapshotError(codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}
	existingSnap, err := NewSnapshotFromId(ctx, snapshotID, client)
	if err != nil {
		return DeleteSnapshotError(codes.Internal, fmt.Sprintln("Failed to initialize snapshot from ID", snapshotID, err.Error()))
	}
	err = existingSnap.Delete(ctx)
	if err != nil {
		return DeleteSnapshotError(codes.Internal, fmt.Sprintln("Failed to delete snapshot", snapshotID, err))
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

func ValidateVolumeCapsError(errorCode codes.Code, errorMessage string) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	glog.ErrorDepth(1, fmt.Sprintln("Error getting volume capabilities, code:", errorCode, ", error:", errorMessage))
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	return &csi.ValidateVolumeCapabilitiesResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (cs *ControllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	glog.V(3).Infof(">>>> Received a ValidateVolumeCapabilities request for volume ID %s", req.GetVolumeId())
	defer glog.V(3).Infof("<<<< Completed processing ValidateVolumeCapabilities request for volume ID %s", req.GetVolumeId())

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return ValidateVolumeCapsError(codes.InvalidArgument, "Volume ID cannot be empty")
	}
	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, req.GetVolumeId())
	}

	client, err := cs.api.GetClientFromSecrets(ctx, req.Secrets)
	if err != nil {
		return ValidateVolumeCapsError(codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}

	volume, err := NewVolumeFromId(ctx, req.GetVolumeId(), client, cs.mounter)
	if err != nil {
		return ValidateVolumeCapsError(codes.Internal, err.Error())
	}
	ok, err := volume.Exists(ctx)
	if err != nil || !ok {
		return ValidateVolumeCapsError(codes.NotFound, fmt.Sprintf("Could not find volume %s", req.GetVolumeId()))
	}

	for _, capability := range req.GetVolumeCapabilities() {
		if capability.GetMount() == nil && capability.GetBlock() == nil {
			return ValidateVolumeCapsError(codes.InvalidArgument, "cannot have both Mount and block access type be undefined")
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
		glog.Infof("Enabling controller service capability: %v", capability.String())
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
