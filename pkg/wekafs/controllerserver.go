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
	"bytes"
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/glog"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
	"strings"
	"sync"
)

const (
	deviceID              = "deviceID"
	defaultFilesystemName = "default"
	maxVolumeIdLength     = 1920
)

type controllerServer struct {
	caps           []*csi.ControllerServiceCapability
	nodeID         string
	gc             *dirVolumeGc
	mounter        *wekaMounter
	creatLock      sync.Mutex
	dynamicVolPath string
	api            *apiStore
}

//goland:noinspection GoUnusedParameter
func (cs *controllerServer) ControllerPublishVolume(c context.Context, request *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	panic("implement me")
}

//goland:noinspection GoUnusedParameter
func (cs *controllerServer) ControllerUnpublishVolume(c context.Context, request *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	panic("implement me")
}

//goland:noinspection GoUnusedParameter
func (cs *controllerServer) ListVolumes(c context.Context, request *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	panic("implement me")
}

//goland:noinspection GoUnusedParameter
func (cs *controllerServer) GetCapacity(c context.Context, request *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	panic("implement me")
}

//goland:noinspection GoUnusedParameter
func (cs *controllerServer) CreateSnapshot(c context.Context, request *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	panic("implement me")
}

//goland:noinspection GoUnusedParameter
func (cs *controllerServer) DeleteSnapshot(c context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	panic("implement me")
}

//goland:noinspection GoUnusedParameter
func (cs *controllerServer) ListSnapshots(c context.Context, request *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	panic("implement me")
}

//goland:noinspection GoUnusedParameter
func (cs *controllerServer) ControllerGetVolume(context.Context, *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	panic("implement me")
}

func NewControllerServer(nodeID string, api *apiStore, mounter *wekaMounter, gc *dirVolumeGc, dynamicVolPath string) *controllerServer {
	return &controllerServer{
		caps: getControllerServiceCapabilities(
			[]csi.ControllerServiceCapability_RPC_Type{
				csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
				csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
			}),
		nodeID:         nodeID,
		mounter:        mounter,
		gc:             gc,
		dynamicVolPath: dynamicVolPath,
		api:            api,
	}
}

func createKeyValuePairs(m map[string]string) string {
	b := new(bytes.Buffer)
	for key, value := range m {
		_, _ = fmt.Fprintf(b, "%s=\"%s\"\n", key, value)
	}
	return b.String()
}

func CreateVolumeError(errorCode codes.Code, errorMessage string) (*csi.CreateVolumeResponse, error) {
	glog.Errorln("Error creating volume, code:", errorCode, ", error:", errorMessage)
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	return &csi.CreateVolumeResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (cs *controllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	glog.V(3).Infof("Received a CreateVolume request: %s", createKeyValuePairs(req.GetParameters()))
	defer glog.V(3).Infof("Completed processing request: %s", createKeyValuePairs(req.GetParameters()))
	cs.creatLock.Lock()
	defer cs.creatLock.Unlock()
	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		glog.V(3).Infof("invalid create volume req: %v", req)
		return nil, err
	}

	// Check arguments
	if len(req.GetName()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Name missing in request")
	}
	caps := req.GetVolumeCapabilities()
	if caps == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capabilities missing in request")
	}

	// Need to calculate volumeID first thing due to possible mapping to multiple FSes
	volumeID, err := createVolumeIdFromRequest(req, cs.dynamicVolPath)
	if err != nil {
		return CreateVolumeError(codes.InvalidArgument, "Failed to resolve VolumeType from CreateVolumeRequest")
	}

	// Validate access type in request
	for _, capability := range caps {
		if capability.GetBlock() != nil {
			return CreateVolumeError(codes.InvalidArgument, "Block accessType is unsupported")
		}
	}

	// obtain client for volume
	client, err := cs.api.GetClientFromSecrets(req.Secrets)
	if err != nil {
		return CreateVolumeError(codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}

	volume, err := NewVolume(volumeID, client)
	if err != nil {
		return CreateVolumeError(codes.Internal, err.Error())
	}

	// Perform mount in order to be able to access Xattrs and get a full volume root path
	mountPoint, err, unmount := volume.Mount(cs.mounter, true)
	defer unmount()
	if err != nil {
		return CreateVolumeError(codes.Internal, err.Error())
	}
	volPath := volume.getFullPath(mountPoint)

	// Check for maximum available capacity
	capacity := req.GetCapacityRange().GetRequiredBytes()

	// If directory already exists, return the create response for idempotence if size matches, or error
	volExists, err := volume.Exists(mountPoint)
	if err != nil {
		return CreateVolumeError(codes.Internal, fmt.Sprintln("Could not check if volume exists", volPath))
	}
	if volExists {
		glog.V(3).Infof("Directory already exists: %v", volPath)

		currentCapacity, err := volume.GetCapacity(mountPoint)
		if err != nil {
			return CreateVolumeError(codes.Internal, err.Error())
		}
		// TODO: Once we have everything working - review this, big potential of race of several CreateVolume requests
		if currentCapacity != capacity && currentCapacity != 0 {
			return CreateVolumeError(codes.AlreadyExists,
				fmt.Sprintf("Volume with same ID exists with different capacity volumeID %s: [current]%d!=%d[requested]",
					volumeID, currentCapacity, capacity))
		}
		return &csi.CreateVolumeResponse{
			Volume: &csi.Volume{
				VolumeId:      volumeID,
				CapacityBytes: req.GetCapacityRange().GetRequiredBytes(),
				VolumeContext: req.GetParameters(),
			},
		}, nil
	}

	// validate minimum capacity before create new volume
	maxStorageCapacity, err := volume.getMaxCapacity(mountPoint)
	if err != nil {
		return CreateVolumeError(codes.Internal, fmt.Sprintf("Cannot obtain free capacity for volume %s", volumeID))
	}
	if capacity > maxStorageCapacity {
		return CreateVolumeError(codes.OutOfRange, fmt.Sprintf("Requested capacity %d exceeds maximum allowed %d", capacity, maxStorageCapacity))
	}

	// Actually try to create the volume here
	enforceCapacity, err := getStrictCapacityFromParams(req.GetParameters())
	if err != nil {
		return CreateVolumeError(codes.Internal, err.Error())
	}
	if err := volume.Create(mountPoint, enforceCapacity, capacity); err != nil {
		return &csi.CreateVolumeResponse{}, err
	}

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volumeID,
			CapacityBytes: req.GetCapacityRange().GetRequiredBytes(),
			VolumeContext: req.GetParameters(),
		},
	}, nil
}

func getStrictCapacityFromParams(params map[string]string) (bool, error) {
	qt := params["capacityEnforcement"]
	enforceCapacity := true
	switch apiclient.QuotaType(qt) {
	case apiclient.QuotaTypeSoft:
		enforceCapacity = false
	case apiclient.QuotaTypeHard:
		enforceCapacity = true
	case "":
		enforceCapacity = false
	default:
		glog.Warningf("Could not recognize capacity enforcement in params: %s", qt)
		return false, errors.New("unsupported capacityEnforcement in volume params")
	}
	return enforceCapacity, nil
}

func DeleteVolumeError(errorCode codes.Code, errorMessage string) (*csi.DeleteVolumeResponse, error) {
	glog.Errorln("Error deleting volume, code:", errorCode, ", error:", errorMessage)
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	return &csi.DeleteVolumeResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (cs *controllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	glog.V(3).Infof("Received a DeleteVolume request for volume ID %s", req.GetVolumeId())
	defer glog.V(3).Infof("Completed processing DeleteVolume request for volume ID %s", req.GetVolumeId())
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	client, err := cs.api.GetClientFromSecrets(req.Secrets)
	if err != nil {
		return DeleteVolumeError(codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}

	volume, err := NewVolume(volumeID, client)
	if err != nil {
		// Should return ok on incorrect ID (by CSI spec)
		return &csi.DeleteVolumeResponse{}, nil
	}

	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		glog.V(3).Infof("invalid delete volume req: %v", req)
		return DeleteVolumeError(codes.Internal, err.Error())
	}

	err = volume.moveToTrash(cs.mounter, cs.gc)
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
	glog.Errorln("Error expanding volume, code:", errorCode, ", error:", errorMessage)
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	return &csi.ControllerExpandVolumeResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (cs *controllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	glog.V(3).Infof("Received a ControllerExpandVolume request for volume ID %s", req.GetVolumeId())
	defer glog.V(3).Infof("Completed processing ControllerExpandVolume request for volume ID %s", req.GetVolumeId())

	if len(req.GetVolumeId()) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "Volume ID not specified")
	}
	client, err := cs.api.GetClientFromSecrets(req.Secrets)
	if err != nil {
		return ExpandVolumeError(codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}

	volume, err := NewVolume(req.GetVolumeId(), client)
	if err != nil {
		return ExpandVolumeError(codes.NotFound, fmt.Sprintf("Volume with id %s does not exist", req.GetVolumeId()))
	}

	capRange := req.GetCapacityRange()
	if capRange == nil {
		return ExpandVolumeError(codes.InvalidArgument, "Capacity range not provided")
	}

	// Perform mount in order to be able to access Xattrs and get a full volume root path
	mountPoint, err, unmount := volume.Mount(cs.mounter, true)
	defer unmount()
	if err != nil {
		return ExpandVolumeError(codes.Internal, err.Error())
	}

	capacity := capRange.GetRequiredBytes()

	maxStorageCapacity, err := volume.getMaxCapacity(mountPoint)
	if err != nil {
		return ExpandVolumeError(codes.Unknown, fmt.Sprintf("Cannot obtain free capacity for volume %s", volume.GetId()))
	}
	if capacity > maxStorageCapacity {
		return ExpandVolumeError(codes.OutOfRange, fmt.Sprintf("Requested capacity %d exceeds maximum allowed %d", capacity, maxStorageCapacity))
	}

	ok, err := volume.Exists(mountPoint)
	if err != nil {
		return ExpandVolumeError(codes.Internal, err.Error())
	}
	if !ok {
		return ExpandVolumeError(codes.Internal, "Volume does not exist")
	}

	currentSize, err := volume.GetCapacity(mountPoint)
	if err != nil {
		return ExpandVolumeError(codes.Internal, "Could not get volume capacity")
	}
	glog.Infof("Volume %s: current capacity: %d, expanding to %d", volume.GetId(), currentSize, capacity)

	if currentSize != capacity {
		if err := volume.UpdateCapacity(mountPoint, nil, capacity); err != nil {
			return ExpandVolumeError(codes.Internal, fmt.Sprintf("Could not update volume %s: %v", volume, err))
		}
	}
	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         capacity,
		NodeExpansionRequired: false, // since this is filesystem, no need to resize on node
	}, nil
}

//goland:noinspection GoUnusedParameter
func (cs *controllerServer) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: cs.caps,
	}, nil
}

func ValidateVolumeCapsError(errorCode codes.Code, errorMessage string) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	glog.Errorln("Error getting volume capabilities, code:", errorCode, ", error:", errorMessage)
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	return &csi.ValidateVolumeCapabilitiesResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (cs *controllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	glog.V(3).Infof("Received a ValidateVolumeCapabilities request for volume ID %s", req.GetVolumeId())
	defer glog.V(3).Infof("Completed processing ValidateVolumeCapabilities request for volume ID %s", req.GetVolumeId())

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return ValidateVolumeCapsError(codes.InvalidArgument, "Volume ID cannot be empty")
	}
	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, req.GetVolumeId())
	}
	// this part must be added to make sure we return NotExists rather than Invalid
	if err := validateVolumeId(req.GetVolumeId()); err != nil {
		return ValidateVolumeCapsError(codes.NotFound, fmt.Sprintf("Volume ID %s not found", req.GetVolumeId()))

	}
	client, err := cs.api.GetClientFromSecrets(req.Secrets)
	if err != nil {
		return ValidateVolumeCapsError(codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}

	volume, err := NewVolume(req.GetVolumeId(), client)
	if err != nil {
		return ValidateVolumeCapsError(codes.Internal, err.Error())
	}
	// TODO: Mount/validate in xattr if there is anything to validate. Right now mounting just to see if folder exists
	mountPoint, err, unmount := volume.Mount(cs.mounter, false)
	defer unmount()
	if err != nil {
		return ValidateVolumeCapsError(codes.Internal, fmt.Sprintf("Could not mount volume %s", req.GetVolumeId()))
	}
	if ok, err2 := volume.Exists(mountPoint); err2 != nil && ok {
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

func (cs *controllerServer) validateControllerServiceRequest(c csi.ControllerServiceCapability_RPC_Type) error {
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
