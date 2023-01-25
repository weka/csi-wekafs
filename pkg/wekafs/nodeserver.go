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
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/glog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/utils/mount"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const TopologyKeyNode = "topology.wekafs.csi/node"
const TopologyLabelNode = "topology.csi.weka.io/node"
const TopologyLabelWeka = "topology.csi.weka.io/global"
const WekaKernelModuleName = "wekafsgw"
const crashOnNoWeka = false

type NodeServer struct {
	caps              []*csi.NodeServiceCapability
	nodeID            string
	maxVolumesPerNode int64
	mounter           *wekaMounter
	api               *ApiStore
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeExpandVolume(ctx context.Context, request *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	panic("implement me")
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeGetVolumeStats(ctx context.Context, request *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	panic("implement me")
}

func NewNodeServer(nodeId string, maxVolumesPerNode int64, api *ApiStore, mounter *wekaMounter) *NodeServer {
	//goland:noinspection GoBoolExpressions
	if mounter.debugPath == "" && !isWekaInstalled() && crashOnNoWeka {
		Die("Weka OS driver module not installed, exiting")
	}
	return &NodeServer{
		caps: getNodeServiceCapabilities(
			[]csi.NodeServiceCapability_RPC_Type{
				//csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
			},
		),
		nodeID:            nodeId,
		maxVolumesPerNode: maxVolumesPerNode,
		mounter:           mounter,
		api:               api,
	}
}

func isWekaInstalled() bool {
	glog.Info("Checking if wekafs is installed on host")
	cmd := fmt.Sprintf("lsmod | grep -w %s", WekaKernelModuleName)
	res, _ := exec.Command("sh", "-c", cmd).Output()
	return strings.Contains(string(res), WekaKernelModuleName)
}

func NodePublishVolumeError(errorCode codes.Code, errorMessage string) (*csi.NodePublishVolumeResponse, error) {
	glog.Errorln("Error publishing volume, code:", errorCode, ", error:", errorMessage)
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	return &csi.NodePublishVolumeResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	glog.V(3).Infof(">>>> Received a NodePublishVolume request for volume ID %s", req.GetVolumeId())
	defer glog.V(3).Infof("<<<< Completed processing NodePublishVolume request for volume ID %s", req.GetVolumeId())
	client, err := ns.api.GetClientFromSecrets(ctx, req.Secrets)
	if err != nil {
		return NodePublishVolumeError(codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}
	volume, err := NewVolumeFromId(ctx, req.GetVolumeId(), client, ns.mounter)
	if err != nil {
		return NodePublishVolumeError(codes.InvalidArgument, err.Error())
	}

	// Check volume capabitily arguments
	if req.GetVolumeCapability() == nil {
		return NodePublishVolumeError(codes.InvalidArgument, "Volume capability missing in request")
	}
	if req.GetVolumeCapability().GetBlock() != nil &&
		req.GetVolumeCapability().GetMount() != nil {
		return NodePublishVolumeError(codes.InvalidArgument, "cannot have both block and Mount access type")
	}

	// check that requested capability is a mount
	if req.GetVolumeCapability().GetBlock() != nil {
		return NodePublishVolumeError(codes.InvalidArgument, "block volume mount not supported")
	}

	// check targetPath
	targetPath := filepath.Clean(req.GetTargetPath())
	mounter := mount.New("")
	if len(targetPath) == 0 {
		return NodePublishVolumeError(codes.InvalidArgument, "Target path missing in request")
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

	glog.V(4).Infof("Mount is going to be performed this way:\ntarget %v\nfstype %v\ndevice %v\nreadonly %v\nvolumeId %v\nattributes %v\nmountflags %v\n",
		targetPath, fsType, deviceId, readOnly, volume.GetId(), attrib, mountFlags)

	err, unmount := volume.Mount(ctx, false)
	if err != nil {
		unmount()
		return NodePublishVolumeError(codes.Internal, "Failed to mount a parent filesystem, check Authentication: "+err.Error())
	}
	fullPath := volume.getFullPath(ctx, false)

	glog.Infof("Ensuring target mount root directory exists: %s", filepath.Dir(targetPath))
	if err = os.MkdirAll(filepath.Dir(targetPath), DefaultVolumePermissions); err != nil {
		return NodePublishVolumeError(codes.Internal, err.Error())
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
			return NodePublishVolumeError(codes.Internal, err.Error())
		}
	}

	glog.Infof("Attempting mount bind between volume %s and mount target %s, options: %s", volume.GetId(), targetPath, options)

	// if we run in K8s isolated environment, 2nd mount must be done using mapped volume path
	if err := mounter.Mount(fullPath, targetPath, "", options); err != nil {
		var errList strings.Builder
		errList.WriteString(err.Error())
		unmount() // unmount only if mount bind failed
		return NodePublishVolumeError(codes.Internal, fmt.Sprintf("failed to Mount device: %s at %s: %s", fullPath, targetPath, errList.String()))
	}

	// Not doing unmount, NodePublish should do unmount but only when it unmounts bind successfully
	glog.Infof("Successfully published volume %s", volume.GetId())
	return &csi.NodePublishVolumeResponse{}, nil
}

func NodeUnpublishVolumeError(errorCode codes.Code, errorMessage string) (*csi.NodeUnpublishVolumeResponse, error) {
	glog.Errorln("Error publishing volume, code:", errorCode, ", error:", errorMessage)
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	return &csi.NodeUnpublishVolumeResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	glog.V(3).Infof(">>>> Received a NodeUnpublishVolume request for volume ID %s", req.GetVolumeId())
	defer glog.V(3).Infof("<<<< Completed processing NodeUnpublishVolume request for volume ID %s", req.GetVolumeId())
	// Check arguments

	volume, err := NewVolumeFromId(ctx, req.GetVolumeId(), nil, ns.mounter)
	if err != nil {
		return &csi.NodeUnpublishVolumeResponse{}, err
		//return NodeUnpublishVolumeError(codes.Internal, err.Error())
	}

	if len(req.GetTargetPath()) == 0 {
		return NodeUnpublishVolumeError(codes.InvalidArgument, "Target path missing in request")
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
			return NodeUnpublishVolumeError(codes.Internal, "unexpected situation, please contact support")
		}

	}
	// check if this path is a wekafs mount
	if ns.mounter.debugPath == "" {
		if PathIsWekaMount(targetPath) {
			glog.Infof("Directory %s exists and is weka mount [%s]", targetPath, volume.GetId())
		} else {
			msg := fmt.Sprintf("Directory %s exists, but not a weka mount", targetPath)
			glog.Info()
			return NodeUnpublishVolumeError(codes.Internal, msg)
		}
	}

	glog.V(2).Infof("Attempting to perform unmount of target path %s", targetPath)
	if err := mount.New("").Unmount(targetPath); err != nil {
		//it seems that when NodeUnpublishRequest appears, this target path is already not existing, e.g. due to pod being deleted
		glog.Errorf("failed unmounting volume %s at %s : %s", volume.GetId(), targetPath, err)
		return NodeUnpublishVolumeError(codes.Internal, err.Error())
	} else {
		glog.Infof("Successfully unmounted %s", targetPath)
	}

	glog.Infof("Attempting to remove target path %s [%s]", targetPath, volume.GetId())
	if err := os.Remove(targetPath); err != nil {
		return NodeUnpublishVolumeError(codes.Internal, err.Error())
	}

	glog.V(4).Infof("wekafs: volume %s has been unpublished.", volume.GetId())
	// Doing this only in case both bind unmount and remove succeeded
	glog.Infof("Calling decrease refcount on mount %s", volume.GetId())
	err = volume.Unmount(ctx, false)
	if err != nil {
		glog.Errorf("Post-unpublish unmount failed %s", err)
	}
	glog.Infof("Successfully unpublished volume %s", volume.GetId())
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func NodeStageVolumeError(errorCode codes.Code, errorMessage string) (*csi.NodeStageVolumeResponse, error) {
	glog.Errorln("Error staging volume on node, code:", errorCode, ", error:", errorMessage)
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	return &csi.NodeStageVolumeResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	glog.V(3).Infof("Received a NodeStageVolume request for volume ID %s", req.GetVolumeId())
	defer glog.V(3).Infof("Completed processing NodeStageVolume request for volume ID %s", req.GetVolumeId())
	client, err := ns.api.GetClientFromSecrets(ctx, req.Secrets)
	if err != nil {
		return NodeStageVolumeError(codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}
	volume, err := NewVolumeFromId(ctx, req.GetVolumeId(), client, ns.mounter)
	if err != nil {
		return NodeStageVolumeError(codes.Internal, err.Error())
	}
	// Check arguments
	if len(req.GetStagingTargetPath()) == 0 {
		return NodeStageVolumeError(codes.InvalidArgument, "Target path missing in request")
	}

	if req.GetVolumeCapability() == nil {
		return NodeStageVolumeError(codes.InvalidArgument, "Error occured, volume Capability missing in request")
	}

	if req.GetVolumeCapability().GetBlock() != nil {
		return NodeStageVolumeError(codes.InvalidArgument, "Block accessType is unsupported")
	}
	glog.V(4).Infof("wekafs: volume %s has been staged.", volume.GetId())

	return &csi.NodeStageVolumeResponse{}, nil
}

func NodeUnstageVolumeError(errorCode codes.Code, errorMessage string) (*csi.NodeUnstageVolumeResponse, error) {
	glog.Errorln("Error UNstaging volume on node, code:", errorCode, ", error:", errorMessage)
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	return &csi.NodeUnstageVolumeResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {

	// Check arguments
	volume, err := NewVolumeFromId(ctx, req.GetVolumeId(), nil, ns.mounter)
	if err != nil {
		return NodeUnstageVolumeError(codes.Internal, err.Error())
	}

	if len(req.GetStagingTargetPath()) == 0 {
		return NodeUnstageVolumeError(codes.InvalidArgument, "Target path missing in request")
	}
	glog.V(4).Infof("wekafs: volume %s has been unstaged.", volume.GetId())
	return &csi.NodeUnstageVolumeResponse{}, nil
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	topology := &csi.Topology{
		Segments: map[string]string{
			TopologyKeyNode:   ns.nodeID, // required exactly same way as this is how node is accessed by K8s
			TopologyLabelNode: ns.nodeID,
			TopologyLabelWeka: "true",
		},
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
