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
	"fmt"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/utils/mount"
)

const TopologyKeyNode = "topology.wekafs.csi/node"
const WekaModule = "wekafsgw"
const crashOnNoWeka = false

type nodeServer struct {
	caps              []*csi.NodeServiceCapability
	nodeID            string
	maxVolumesPerNode int64
	mounter           *wekaMounter
	gc                *dirVolumeGc
	api               *apiStore
}

func (ns *nodeServer) NodeExpandVolume(c context.Context, request *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	panic("implement me")
}

func (ns *nodeServer) NodeGetVolumeStats(ctx context.Context, request *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	panic("implement me")
}

//func (ns *nodeServer) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
//
//	if len(req.GetVolumeId()) == 0 {
//		return nil, status.Errorf(codes.InvalidArgument, "Volume ID not specified")
//	}
//	req.S
//	volume, err := NewVolume(req.GetVolumeId(), nil)
//	if err != nil {
//		return nil, status.Errorf(codes.NotFound, "Volume with id %s does not exist", req.GetVolumeId())
//	}
//
//	capRange := req.GetCapacityRange()
//	if capRange == nil {
//		return nil, status.Error(codes.InvalidArgument, "Capacity range not provided")
//	}
//
//	// Perform mount in order to be able to access Xattrs and get a full volume root path
//	mountPoint, err, unmount := ns.mounter.MountXattr(volume.Filesystem)
//	defer unmount()
//	if err != nil {
//		return nil, err
//	}
//	volPath := volume.getFullPath(mountPoint)
//
//	capacity := int64(capRange.GetRequiredBytes())
//
//	maxStorageCapacity, err := getMaxDirCapacity(mountPoint)
//	if err != nil {
//		return nil, status.Errorf(codes.Unknown, "Cannot obtain free capacity for volume %s", volume)
//	}
//	if capacity > maxStorageCapacity {
//		return nil, status.Errorf(codes.OutOfRange, "Requested capacity %d exceeds maximum allowed %d", capacity, maxStorageCapacity)
//	}
//
//	if volPath, err = validatedVolume(mountPoint, err, volume); err != nil {
//		return nil, err
//	}
//
//	currentSize := getVolumeSize(volPath)
//	glog.Infof("Volume %s: current capacity: %d, expanding to %d", volume.id, currentSize, capacity)
//	if currentSize < capacity {
//		if err := updateDirCapacity(volPath, capacity); err != nil {
//			return nil, status.Errorf(codes.Internal, "Could not update volume %s: %v", volume, err)
//		}
//	}
//
//	return &csi.NodeExpandVolumeResponse{
//		CapacityBytes: capacity,
//	}, nil
//}

func NewNodeServer(nodeId string, maxVolumesPerNode int64, api *apiStore, mounter *wekaMounter, gc *dirVolumeGc) *nodeServer {
	if mounter.debugPath == "" && !isWekaInstalled() && crashOnNoWeka {
		exitMsg := "weka OS driver module not installed, exiting"
		_ = ioutil.WriteFile("/dev/termination-log", []byte(exitMsg), 0644)
		panic(exitMsg)
	}
	return &nodeServer{
		caps: getNodeServiceCapabilities(
			[]csi.NodeServiceCapability_RPC_Type{
				//csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
			},
		),
		nodeID:            nodeId,
		maxVolumesPerNode: maxVolumesPerNode,
		mounter:           mounter,
		gc:                gc,
		api:               api,
	}
}

func isWekaInstalled() bool {
	glog.Info("Checking if wekafs is installed on host")
	cmd := fmt.Sprintf("lsmod | grep -w %s", WekaModule)
	res, _ := exec.Command("sh", "-c", cmd).Output()
	return strings.Contains(string(res), WekaModule)
}

func (ns *nodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	glog.Infof("Received a NodePublishVolumeRequest %s", req)
	client, err := ns.api.GetClientFromSecrets(req.Secrets)
	if err != nil {
		return &csi.NodePublishVolumeResponse{}, status.Errorf(codes.Internal, "Failed to initialize Weka API client for the request")
	}
	volume, err := NewVolume(req.GetVolumeId(), client)
	if err != nil {
		return &csi.NodePublishVolumeResponse{}, err
	}

	// Check volume capabitily arguments
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability missing in request")
	}
	if req.GetVolumeCapability().GetBlock() != nil &&
		req.GetVolumeCapability().GetMount() != nil {
		return nil, status.Error(codes.InvalidArgument, "cannot have both block and Mount access type")
	}

	// check that requested capability is a mount
	if req.GetVolumeCapability().GetBlock() != nil {
		return nil, status.Error(codes.InvalidArgument, "block volume mount not supported")
	}

	// check targetPath
	targetPath := filepath.Clean(req.GetTargetPath())
	mounter := mount.New("")
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}

	fsType := req.GetVolumeCapability().GetMount().GetFsType()

	deviceId := ""
	if req.GetPublishContext() != nil {
		deviceId = req.GetPublishContext()[deviceID]
	}
	var options []string
	readOnly := req.GetReadonly()

	if readOnly {
		options = []string{"ro", "bind"}
	} else {
		options = []string{"bind"}
	}

	attrib := req.GetVolumeContext()
	mountFlags := req.GetVolumeCapability().GetMount().GetMountFlags()

	glog.V(4).Infof("target %v\nfstype %v\ndevice %v\nreadonly %v\nvolumeId %v\nattributes %v\nmountflags %v\n",
		targetPath, fsType, deviceId, readOnly, volume.GetId(), attrib, mountFlags)

	mountPoint, err, unmount := volume.Mount(ns.mounter, false)
	ok, err := volume.Exists(mountPoint)
	if err != nil {
		return &csi.NodePublishVolumeResponse{}, status.Error(codes.Internal, err.Error())
	}
	if !ok {
		unmount()
		return &csi.NodePublishVolumeResponse{}, err
	}
	fullPath := volume.getFullPath(mountPoint)

	glog.Infof("Ensuring target mount root directory exists: %s", filepath.Dir(targetPath))
	if err = os.MkdirAll(filepath.Dir(targetPath), 0750); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	glog.Infof("Ensuring mount target directory exists: %s", targetPath)
	if err = os.Mkdir(targetPath, 0750); err != nil {
		// If failed to create directory - other call succeded and not this one,
		// TODO: Returning success, but this is not completely right.
		// As potentially some other process holds. Need a good way to inspect binds
		// SearchMountPoints and GetMountRefs failed to do the job
		if os.IsExist(err) {
			if ns.mounter.debugPath == "" {
				if PathIsWekaMount(targetPath) {
					glog.Infof("Target path directory %s already exists and is a Weka filesystem mount", targetPath)
					unmount()
					return &csi.NodePublishVolumeResponse{}, nil
				} else {
					glog.Infof("Target path directory %s already exists but is not mounted", targetPath)
				}
			} else {
				glog.Infof("Assuming debug execution and not validating WekaFS mount")
				unmount()
				return &csi.NodePublishVolumeResponse{}, nil
			}

		} else {
			glog.Errorf("Target path directory %s could not be created, %s", targetPath, err)
			unmount()
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	glog.Infof("Attempting mount bind between volume %s and mount target %s, options: %s", volume.GetId(), targetPath, options)

	// if we run in K8s isolated environment, 2nd mount must be done using mapped volume path
	if err := mounter.Mount(fullPath, targetPath, "", options); err != nil {
		var errList strings.Builder
		errList.WriteString(err.Error())
		unmount() // unmount only if mount bind failed
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to Mount device: %s at %s: %s", fullPath, targetPath, errList.String()))
	}

	// Not doing unmount, NodePublish should do unmount but only when it unmounts bind succesffully
	glog.Infof("Successfully published volume %s", volume.GetId())
	return &csi.NodePublishVolumeResponse{}, nil
}

func (ns *nodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	glog.Infof("Received NodeUnpublishVolume request %s", req)
	// Check arguments

	volume, err := NewVolume(req.GetVolumeId(), nil)
	if err != nil {
		return &csi.NodeUnpublishVolumeResponse{}, err
	}

	if len(req.GetTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}
	targetPath := req.GetTargetPath()

	// TODO: Verify that targetPath is indeed equals to expected source of bind mount
	//		 Which is not straightforward in case plugin was restarted, as in this case
	//		 we lose information of source. Probably Context can be used
	glog.V(2).Infof("Checking if target path %s exists", targetPath)
	if _, err := os.Stat(targetPath); err != nil {
		if os.IsNotExist(err) {
			glog.Warningf("Seems like volume %s is not published under target path %s, assuming repeating unpublish request", volume.GetId(), targetPath)
			return &csi.NodeUnpublishVolumeResponse{}, nil
		} else {
			return &csi.NodeUnpublishVolumeResponse{}, status.Errorf(codes.Internal, " unexpected situation")
		}

	}
	// check if this path is a wekafs mount
	if ns.mounter.debugPath == "" {
		if PathIsWekaMount(targetPath) {
			glog.Infof("Directory %s exists and is weka mount [%s]", targetPath, volume.GetId())
		} else {
			msg := fmt.Sprintf("Directory %s exists, but not a weka mount", targetPath)
			glog.Info()
			return nil, status.Error(codes.Internal, msg)
		}
	}

	glog.V(2).Infof("Attempting to perform unmount of target path %s", targetPath)
	if err := mount.New("").Unmount(targetPath); err != nil {
		//it seems that when NodeUnpublishRequest appears, this target path is already not existing, e.g. due to pod being deleted
		glog.Errorf("failed unmounting volume %s at %s : %s", volume.GetId(), targetPath, err)
		return nil, status.Error(codes.Internal, err.Error())
	} else {
		glog.Infof("Successfully unmounted %s", targetPath)
	}

	glog.Infof("Attempting to remove target path %s [%s]", targetPath, volume.GetId())
	if err := os.Remove(targetPath); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	glog.V(4).Infof("wekafs: volume %s has been unpublished.", volume.GetId())
	// Doing this only in case both bind unmount and remove succeeded
	glog.Infof("Calling decrease refcount on mount %s", volume.GetId())
	err = volume.Unmount(ns.mounter)
	if err != nil {
		glog.Errorf("Post-unpublish unmount failed %s", err)
	}
	glog.Infof("Successfully unpublished volume %s", volume.GetId())
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (ns *nodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	client, err := ns.api.GetClientFromSecrets(req.Secrets)
	if err != nil {
		return &csi.NodeStageVolumeResponse{}, status.Errorf(codes.Internal, "Failed to initialize Weka API client for the request")
	}
	volume, err := NewVolume(req.GetVolumeId(), client)
	if err != nil {
		return &csi.NodeStageVolumeResponse{}, err
	}
	// Check arguments
	if len(req.GetStagingTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}

	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "Error occured, volume Capability missing in request")
	}

	if req.GetVolumeCapability().GetBlock() != nil {
		return nil, status.Error(codes.InvalidArgument, "Block accessType is unsupported")
	}
	glog.V(4).Infof("wekafs: volume %s has been staged.", volume.GetId())

	return &csi.NodeStageVolumeResponse{}, nil
}

func (ns *nodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {

	// Check arguments
	volume, err := NewVolume(req.GetVolumeId(), nil)
	if err != nil {
		return &csi.NodeUnstageVolumeResponse{}, err
	}

	if len(req.GetStagingTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}
	glog.V(4).Infof("wekafs: volume %s has been unstaged.", volume.GetId())

	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (ns *nodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {

	topology := &csi.Topology{
		Segments: map[string]string{TopologyKeyNode: ns.nodeID},
	}

	return &csi.NodeGetInfoResponse{
		NodeId:             ns.nodeID,
		MaxVolumesPerNode:  ns.maxVolumesPerNode,
		AccessibleTopology: topology,
	}, nil
}

func (ns *nodeServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {

	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: ns.caps,
	}, nil
}

func getNodeServiceCapabilities(nl []csi.NodeServiceCapability_RPC_Type) []*csi.NodeServiceCapability {
	var nsc []*csi.NodeServiceCapability

	for _, capability := range nl {
		glog.Infof("Enabling node service capability: %v", capability.String())
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
