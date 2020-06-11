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
	"strings"

	"github.com/golang/glog"
	"golang.org/x/net/context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/kubernetes/pkg/util/mount"
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
	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}

	// check that mount target exists (or create it) and is not a mount point already
	//TODO: Add support for -o ro, i.e Readonly volumes
	notMnt, err := mount.New("").IsNotMountPoint(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err = os.MkdirAll(targetPath, 0750); err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			notMnt = true
		} else {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	if !notMnt {
		// already mounted, no need to do anything more
		return &csi.NodePublishVolumeResponse{}, nil
	}

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
	mounter := mount.New("")
	fsName := volume.fs
	mountPoint, err, _ := ns.mounter.Mount(fsName)
	fullPath := GetVolumeFullPath(mountPoint, volume.id)

	if err := mounter.Mount(fullPath, targetPath, "", options); err != nil {
		var errList strings.Builder
		errList.WriteString(err.Error())
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to Mount device: %s at %s: %s", fullPath, targetPath, errList.String()))
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (ns *nodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {

	// Check arguments
	volume, err := NewVolume(req.GetVolumeId())
	if err != nil {
		return &csi.NodeUnpublishVolumeResponse{}, err
	}

	if len(req.GetTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}
	targetPath := req.GetTargetPath()

	// Unmount only if the target path is really a Mount point.
	if notMnt, err := mount.IsNotMountPoint(mount.New(""), targetPath); err != nil {
		if !os.IsNotExist(err) {
			return nil, status.Error(codes.Internal, err.Error())
		}
	} else if !notMnt {
		// Unmounting the image or filesystem.
		err = mount.New("").Unmount(targetPath)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	// Delete the Mount point.
	// Does not return error for non-existent path, repeated calls OK for idempotency.
	//TODO: IMPORTANT!
	//		Ensure that path is no longer mount point
	//		and is empty dir and remove dir, not recursive removal
	//		Also, we can have race here with another pod mounting same volume
	//		So this must be under a lock, common with PublishVolume
	if err := os.RemoveAll(targetPath); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	glog.V(4).Infof("wekafs: volume %s has been unpublished.", volume.id)

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
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
					},
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
