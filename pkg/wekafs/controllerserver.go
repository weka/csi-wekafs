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
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
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
}

func (cs *controllerServer) ControllerPublishVolume(c context.Context, request *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	panic("implement me")
}

func (cs *controllerServer) ControllerUnpublishVolume(c context.Context, request *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	panic("implement me")
}

func (cs *controllerServer) ListVolumes(c context.Context, request *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	panic("implement me")
}

func (cs *controllerServer) GetCapacity(c context.Context, request *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	panic("implement me")
}

func (cs *controllerServer) CreateSnapshot(c context.Context, request *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	panic("implement me")
}

func (cs *controllerServer) DeleteSnapshot(c context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	panic("implement me")
}

func (cs *controllerServer) ListSnapshots(c context.Context, request *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	panic("implement me")
}

func NewControllerServer(nodeID string, mounter *wekaMounter, gc *dirVolumeGc, dynamicVolPath string) *controllerServer {
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
	}
}

func createKeyValuePairs(m map[string]string) string {
	b := new(bytes.Buffer)
	for key, value := range m {
		fmt.Fprintf(b, "%s=\"%s\"\n", key, value)
	}
	return b.String()
}

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
		return &csi.CreateVolumeResponse{}, status.Errorf(codes.InvalidArgument, "Failed to resolve VolumeType from CreateVolumeRequest")
	}
	volume, err := NewVolume(volumeID)
	if err != nil {
		return &csi.CreateVolumeResponse{}, err
	}
	// Validate access type in request
	for _, capability := range caps {
		if capability.GetBlock() != nil {
			return nil, status.Error(codes.InvalidArgument, "Block accessType is unsupported")
		}
	}

	// Perform mount in order to be able to access Xattrs and get a full volume root path
	mountPoint, err, unmount := cs.mounter.MountXattr(volume.fs)
	defer unmount()
	if err != nil {
		return nil, err
	}
	volPath := volume.getFullPath(mountPoint)

	// Check for maximum available capacity
	capacity := req.GetCapacityRange().GetRequiredBytes()

	// If directory already exists, return the create response for idempotence if size matches, or error
	if PathExists(volPath) {
		glog.V(3).Infof("Directory already exists: %v", volPath)

		currentCapacity := getVolumeSize(volPath)
		// TODO: Once we have everything working - review this, big potential of race of several CreateVolume requests
		if currentCapacity != capacity && currentCapacity != 0 {
			return nil, status.Errorf(codes.AlreadyExists, "Volume with same ID exists with different capacity volumeID %s: [current]%d!=%d[requested]", volumeID, currentCapacity, capacity)
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
	maxStorageCapacity, err := getMaxDirCapacity(mountPoint)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Cannot obtain free capacity for volume %s", volumeID)
	}
	if capacity > maxStorageCapacity {
		return nil, status.Errorf(codes.OutOfRange, "Requested capacity %d exceeds maximum allowed %d", capacity, maxStorageCapacity)
	}

	// Actually try to create the volume here
	if err = os.MkdirAll(volPath, 0750); err != nil {
		glog.Errorf("Failed to create directory %s", volPath)
		return nil, err
	}
	glog.V(3).Infof("Created volume %s in: %v", volume.id, volPath)
	// Update volume metadata on directory using xattrs
	if err := setVolumeProperties(volPath, capacity, req.GetName()); err != nil {
		// attempt clean up in such case
		_ = os.RemoveAll(volPath)
		glog.Warningf("Removed previously created volume %s in %s due to error setting attrs", volume.id, volPath)
		return nil, err
	} else {
		glog.Infof("Volume %s: set volume properties to %s", volumeID)
	}

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volumeID,
			CapacityBytes: req.GetCapacityRange().GetRequiredBytes(),
			VolumeContext: req.GetParameters(),
		},
	}, nil
}

func (cs *controllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	volume, err := NewVolume(volumeID)
	if err != nil {
		return &csi.DeleteVolumeResponse{}, nil // Should return invalid on incorrect ID
	}

	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		glog.V(3).Infof("invalid delete volume req: %v", req)
		return nil, err
	}

	// Perform mount in order to be able to access Xattrs and get a full volume root path
	glog.V(4).Infof("Deleting volume %s, located in filesystem %s", volume.id, volume.fs)

	err = volume.moveToTrash(cs.mounter, cs.gc)
	if os.IsNotExist(err) {
		glog.V(4).Infof("Volume not found %s, but returning success for idempotence", volume.id)
		return &csi.DeleteVolumeResponse{}, nil
	}
	return &csi.DeleteVolumeResponse{}, err
}

func (cs *controllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {

	if len(req.GetVolumeId()) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "Volume ID not specified")
	}
	volume, err := NewVolume(req.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "Volume with id %s does not exist", req.GetVolumeId())
	}

	capRange := req.GetCapacityRange()
	if capRange == nil {
		return nil, status.Error(codes.InvalidArgument, "Capacity range not provided")
	}

	// Perform mount in order to be able to access Xattrs and get a full volume root path
	mountPoint, err, unmount := cs.mounter.MountXattr(volume.fs)
	defer unmount()
	if err != nil {
		return nil, err
	}
	volPath := volume.getFullPath(mountPoint)

	capacity := int64(capRange.GetRequiredBytes())

	maxStorageCapacity, err := getMaxDirCapacity(mountPoint)
	if err != nil {
		return nil, status.Errorf(codes.Unknown, "Cannot obtain free capacity for volume %s", volume)
	}
	if capacity > maxStorageCapacity {
		return nil, status.Errorf(codes.OutOfRange, "Requested capacity %d exceeds maximum allowed %d", capacity, maxStorageCapacity)
	}

	if volPath, err = validatedVolume(mountPoint, err, volume); err != nil {
		return nil, err
	}

	currentSize := getVolumeSize(volPath)
	glog.Infof("Volume %s: current capacity: %d, expanding to %d", volume.id, currentSize, capacity)
	if currentSize < capacity {
		if err := updateDirCapacity(volPath, capacity); err != nil {
			return nil, status.Errorf(codes.Internal, "Could not update volume %s: %v", volume, err)
		}
	}

	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         capacity,
		NodeExpansionRequired: true, // since this is filesystem, no need to resize on node
	}, nil
}

func (cs *controllerServer) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: cs.caps,
	}, nil
}

func (cs *controllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return &csi.ValidateVolumeCapabilitiesResponse{}, status.Error(codes.InvalidArgument, "Volume ID cannot be empty")
	}
	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, req.VolumeId)
	}
	// this part must be added to make sure we return NotExists rather than Invalid
	if err := validateVolumeId(req.GetVolumeId()); err != nil {
		return &csi.ValidateVolumeCapabilitiesResponse{}, status.Errorf(codes.NotFound, "Volume ID %s not found", req.GetVolumeId())

	}
	volume, err := NewVolume(req.GetVolumeId())
	if err != nil {
		return &csi.ValidateVolumeCapabilitiesResponse{}, err
	}
	// TODO: Mount/validate in xattr if there is anything to validate. Right now mounting just to see if folder exists
	mountPoint, err, unmount := cs.mounter.Mount(volume.fs)
	defer unmount()
	if _, err := validatedVolume(mountPoint, err, volume); err != nil {
		return &csi.ValidateVolumeCapabilitiesResponse{}, status.Errorf(codes.NotFound, "Could not find volume %s", req.VolumeId)
	}

	for _, capability := range req.GetVolumeCapabilities() {
		if capability.GetMount() == nil && capability.GetBlock() == nil {
			return nil, status.Error(codes.InvalidArgument, "cannot have both Mount and block access type be undefined")
		}
		// A real driver would check the capabilities of the given volume with
		// the set of requested capabilities.
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
