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
	"os"
	"path/filepath"
	"strings"

	"github.com/golang/glog"
	"golang.org/x/net/context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/utils/mount"
)

const TopologyKeyNode = "topology.wekafs.csi/node"

type nodeServer struct {
	nodeID            string
	maxVolumesPerNode int64
	mounter           *wekaMounter
	gc                *dirVolumeGc
}

func (ns *nodeServer) NodeGetVolumeStats(c context.Context, request *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	panic("implement me")
}

func (ns *nodeServer) NodeExpandVolume(c context.Context, request *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	panic("implement me")
}

func NewNodeServer(nodeId string, maxVolumesPerNode int64, mounter *wekaMounter, gc *dirVolumeGc) *nodeServer {
	return &nodeServer{
		nodeID:            nodeId,
		maxVolumesPerNode: maxVolumesPerNode,
		mounter:           mounter,
		gc:                gc,
	}
}

func (ns *nodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	glog.Infof("Received a NodePublishVolumeRequest %s", req)
	volume, err := NewVolume(req.GetVolumeId())
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

	//// check that mount target exists (or create it) and is not a mount point already
	//TODO: Add support for -o ro, i.e Readonly volumes
	//notMnt, err := mounter.GetMountRefs(targetPath)
	//if err != nil {
	//	if os.IsNotExist(err) {
	//		if err = os.MkdirAll(filepath.Dir(targetPath), 0750); err != nil {
	//			if os.IsExist(err) {
	//				return nil, status.Error(codes.Internal, err.Error())
	//			}
	//		}
	//		if err = os.Mkdir(targetPath, 0750); err != nil {
	//			// If failed to create directory - other call succeded and not this one,
	//			// return error and let it retry if needed
	//			return nil, status.Error(codes.Internal, err.Error())
	//		}
	//		notMnt = true
	//	} else {
	//		return nil, status.Error(codes.Internal, err.Error())
	//	}
	//}

	//if !notMnt {
	//	// already mounted, no need to do anything more
	//	glog.Info("Already mounted, returing success for %s", volume.id)
	//	return &csi.NodePublishVolumeResponse{}, nil
	//}
	//

	fsType := req.GetVolumeCapability().GetMount().GetFsType()

	deviceId := ""
	if req.GetPublishContext() != nil {
		deviceId = req.GetPublishContext()[deviceID]
	}

	readOnly := req.GetReadonly()
	attrib := req.GetVolumeContext()
	mountFlags := req.GetVolumeCapability().GetMount().GetMountFlags()

	glog.V(4).Infof("target %v\nfstype %v\ndevice %v\nreadonly %v\nvolumeId %v\nattributes %v\nmountflags %v\n",
		targetPath, fsType, deviceId, readOnly, volume.id, attrib, mountFlags)

	options := []string{"bind"}
	if readOnly {
		options = append(options, "ro")
	}
	fsName := volume.fs
	mountPoint, err, unmount := ns.mounter.Mount(fsName)
	fullPath := GetVolumeFullPath(mountPoint, volume.id)

	if _, err = validatedVolume(mountPoint, err, volume); err != nil {
		glog.Infof("Volume %s not found on filesystem %s", volume.fs, volume.id)
		unmount()
		return nil, err
	} else {
		glog.Infof("Volume %s was found on filesystem %s", volume.fs, volume.id)
	}

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
			glog.Infof("Target path directory %s already exists, assuming this is a repeating mount request", targetPath)
			unmount()
			return &csi.NodePublishVolumeResponse{}, nil
		} else {
			glog.Errorf("Target path directory %s could not be created, %s", targetPath, err)
			unmount()
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	glog.Infof("Attempting mount bind between volume contents and mount target")

	// if we run in K8s isolated environment, 2nd mount must be done using mapped volume path
	if err := mounter.Mount(fullPath, targetPath, "", options); err != nil {
		var errList strings.Builder
		errList.WriteString(err.Error())
		unmount() // unmount only if mount bind failed
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to Mount device: %s at %s: %s", fullPath, targetPath, errList.String()))
	}

	// Not doing unmount, NodePublish should do unmount but only when it unmounts bind succesffully
	return &csi.NodePublishVolumeResponse{}, nil
}

func (ns *nodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	glog.Infof("Received NodeUnpublishVolume request %s", req)
	// Check arguments
	volume, err := NewVolume(req.GetVolumeId())
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
	glog.Infof("Checking if target path %s exists", targetPath)
	if _, err := os.Stat(targetPath); err != nil {
		if os.IsNotExist(err) {
			glog.Warningf("Seems like volume %s is not published under target path , assuming repeating unpublish request", volume.id, targetPath)
			return &csi.NodeUnpublishVolumeResponse{}, nil
		} else {
			return &csi.NodeUnpublishVolumeResponse{}, status.Errorf(codes.Internal, " unexpected situation")
		}

	} else {
		glog.Infof("Seems like volume %s exists and is published on target path %s", volume.id, targetPath)
	}

	glog.Infof("Attempting to perform unmount of target path %s", targetPath)
	if err := mount.New("").Unmount(targetPath); err != nil {
		//it seems that when NodeUnpublishRequest appears, this target path is already not existing, e.g. due to pod being deleted
		glog.Errorf("failed unmounting volume %s at %s : %s", volume.id, targetPath, err)
	} else {
		glog.Infof("Attempting to remove target path %s", targetPath)
		if err := os.Remove(targetPath); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	glog.V(4).Infof("wekafs: volume %s has been unpublished.", volume.id)
	// Doing this only in case both bind unmount and remove succeeded
	glog.Infof("Calling decrease refcount on mount %s", volume.id)
	err = ns.mounter.Unmount(volume.fs)
	if err != nil {
		glog.Errorf("Post-unpublish unmount failed %s", err)
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (ns *nodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {

	volume, err := NewVolume(req.GetVolumeId())
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
	glog.V(4).Infof("wekafs: volume %s has been staged.", volume.id)

	return &csi.NodeStageVolumeResponse{}, nil
}

func (ns *nodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {

	// Check arguments
	volume, err := NewVolume(req.GetVolumeId())
	if err != nil {
		return &csi.NodeUnstageVolumeResponse{}, err
	}

	if len(req.GetStagingTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}
	glog.V(4).Infof("wekafs: volume %s has been unstaged.", volume.id)

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
		Capabilities: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{},
				},
			},
			//{
			//	Type: &csi.NodeServiceCapability_Rpc{
			//		Rpc: &csi.NodeServiceCapability_RPC{
			//			Type: csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
			//		},
			//	},
			//},
		},
	}, nil
}
