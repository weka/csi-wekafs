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
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/utils/mount"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	TopologyKeyNode                  = "topology.wekafs.csi/node"
	TopologyLabelNode                = "topology.csi.weka.io/node"
	TopologyLabelWeka                = "topology.csi.weka.io/global"
	WekaKernelModuleName             = "wekafsgw"
	crashOnNoWeka                    = false
	NodeServerAdditionalMountOptions = MountOptionSyncOnClose
)

type NodeServer struct {
	caps              []*csi.NodeServiceCapability
	nodeID            string
	maxVolumesPerNode int64
	mounter           *wekaMounter
	api               *ApiStore
	config            *DriverConfig
}

func (ns *NodeServer) getDefaultMountOptions() MountOptions {
	return getDefaultMountOptions().MergedWith(NewMountOptionsFromString(NodeServerAdditionalMountOptions))
}

func (ns *NodeServer) isInDebugMode() bool {
	return ns.getConfig().isInDebugMode()
}

func (ns *NodeServer) getConfig() *DriverConfig {
	return ns.config
}

func (ns *NodeServer) getApiStore() *ApiStore {
	return ns.api
}

func (ns *NodeServer) getMounter() *wekaMounter {
	return ns.mounter
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeExpandVolume(ctx context.Context, request *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	panic("implement me")
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeGetVolumeStats(ctx context.Context, request *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	panic("implement me")
}

func NewNodeServer(nodeId string, maxVolumesPerNode int64, api *ApiStore, mounter *wekaMounter, config *DriverConfig) *NodeServer {
	//goland:noinspection GoBoolExpressions
	if !config.isInDebugMode() && !isWekaInstalled() && crashOnNoWeka {
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
		config:            config,
	}
}

func isWekaInstalled() bool {
	log.Info().Msg("Checking if wekafs is installed on host")
	cmd := fmt.Sprintf("lsmod | grep -w %s", WekaKernelModuleName)
	res, _ := exec.Command("sh", "-c", cmd).Output()
	return strings.Contains(string(res), WekaKernelModuleName)
}

func NodePublishVolumeError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.NodePublishVolumeResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error publishing volume")
	return &csi.NodePublishVolumeResponse{}, err
}

func (ns *NodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	op := "NodePublishVolume"
	volumeID := req.GetVolumeId()
	ctx, span := otel.Tracer(TracerName).Start(ctx, op, trace.WithNewRoot())
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)

	logger := log.Ctx(ctx)
	result := "FAILURE"

	logger.Info().Str("volume_id", volumeID).Msg(">>>> Received request")
	defer func() {
		level := zerolog.InfoLevel
		if result != "SUCCESS" {
			level = zerolog.ErrorLevel
		}
		logger.WithLevel(level).Str("result", result).Msg("<<<< Completed processing request")
	}()

	client, err := ns.api.GetClientFromSecrets(ctx, req.Secrets)
	if err != nil {
		return NodePublishVolumeError(ctx, codes.Internal, fmt.Sprintln("Failed to initialize Weka API client for the request", err))
	}
	volume, err := NewVolumeFromId(ctx, req.GetVolumeId(), client, ns)
	if err != nil {
		return NodePublishVolumeError(ctx, codes.InvalidArgument, err.Error())
	}

	// set volume mountOptions
	params := req.GetVolumeContext()
	if params != nil {
		if mountOptions, ok := params["mountOptions"]; ok {
			logger.Trace().Str("mount_options", mountOptions).Msg("Updating volume mount options")
			volume.setMountOptions(ctx, NewMountOptionsFromString(mountOptions))
		}
	}

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
	var options []string
	readOnly := req.GetReadonly()

	if readOnly {
		options = []string{"ro", "bind"}
	} else {
		options = []string{"bind"}
	}

	attrib := req.GetVolumeContext()
	mountFlags := req.GetVolumeCapability().GetMount().GetMountFlags()

	logger.Debug().Str("target_path", targetPath).
		Str("fs_type", fsType).
		Str("device_id", deviceId).
		Bool("read_only", readOnly).
		Str("volume_id", volumeID).
		Fields(attrib).
		Fields(mountFlags).Msg("Performing mount")

	err, unmount := volume.Mount(ctx, false)
	if err != nil {
		unmount()
		return NodePublishVolumeError(ctx, codes.Internal, "Failed to mount a parent filesystem, check Authentication: "+err.Error())
	}
	fullPath := volume.getFullPath(ctx, false)

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
			if !ns.isInDebugMode() {
				if PathIsWekaMount(ctx, targetPath) {
					log.Ctx(ctx).Trace().Str("target_path", targetPath).Bool("weka_mounted", true).Msg("Target path exists")
					unmount()
					return &csi.NodePublishVolumeResponse{}, nil
				} else {
					log.Ctx(ctx).Trace().Str("target_path", targetPath).Bool("weka_mounted", false).Msg("Target path exists")
				}
			} else {
				log.Ctx(ctx).Trace().Msg("Assuming debug execution and not validating WekaFS mount")
				unmount()
				return &csi.NodePublishVolumeResponse{}, nil
			}

		} else {
			log.Error().Err(err).Str("target_path", targetPath).Msg("Failed creating directory")
			unmount()
			return NodePublishVolumeError(ctx, codes.Internal, err.Error())
		}
	}
	logger.Debug().Str("volume_id", volumeID).Str("target_path", targetPath).Str("source_path", fullPath).Fields(options).Msg("Mounting")

	// if we run in K8s isolated environment, 2nd mount must be done using mapped volume path
	if err := mounter.Mount(fullPath, targetPath, "", options); err != nil {
		var errList strings.Builder
		errList.WriteString(err.Error())
		unmount() // unmount only if mount bind failed
		return NodePublishVolumeError(ctx, codes.Internal, fmt.Sprintf("failed to Mount device: %s at %s: %s", fullPath, targetPath, errList.String()))
	}
	result = "SUCCESS"
	// Not doing unmount, NodePublish should do unmount but only when it unmounts bind successfully
	return &csi.NodePublishVolumeResponse{}, nil
}

func NodeUnpublishVolumeError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.NodeUnpublishVolumeResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error deleting volume")
	return &csi.NodeUnpublishVolumeResponse{}, err
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	op := "NodeUnpublishVolume"
	result := "FAILURE"
	volumeID := req.GetVolumeId()
	ctx, span := otel.Tracer(TracerName).Start(ctx, op, trace.WithNewRoot())
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
	// Check arguments

	volume, err := NewVolumeFromId(ctx, req.GetVolumeId(), nil, ns)
	if err != nil {
		return &csi.NodeUnpublishVolumeResponse{}, err
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
		} else {
			return NodeUnpublishVolumeError(ctx, codes.Internal, "unexpected situation, please contact support")
		}

	}
	// check if this path is a wekafs mount
	if !ns.isInDebugMode() {
		if PathIsWekaMount(ctx, targetPath) {
			logger.Debug().Msg("Directory exists and is weka mount")
		} else {
			msg := fmt.Sprintf("Directory %s exists, but not a weka mount", targetPath)
			return NodeUnpublishVolumeError(ctx, codes.Internal, msg)
		}
	}

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

	logger.Trace().Str("volume_id", volumeID).Msg("Unmounting")
	err = volume.Unmount(ctx, false)
	if err != nil {
		logger.Error().Str("volume_id", volumeID).Err(err).Msg("Post-unpublish task failed")
	}
	result = "SUCCESS"
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func NodeStageVolumeError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.NodeStageVolumeResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error staging volume")
	return &csi.NodeStageVolumeResponse{}, err
}

func (ns *NodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	op := "NodeStageVolume"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op, trace.WithNewRoot())
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)

	volumeId := req.GetVolumeId()
	logger := log.Ctx(ctx)
	result := "FAILURE"
	logger.Info().Str("volume_id", volumeId).Msg(">>>> Received request")
	defer func() {
		level := zerolog.InfoLevel
		if result != "SUCCESS" {
			level = zerolog.ErrorLevel
		}
		logger.WithLevel(level).Str("result", result).Msg("<<<< Completed processing request")
	}()

	// Check arguments
	if len(req.GetStagingTargetPath()) == 0 {
		return NodeStageVolumeError(ctx, codes.InvalidArgument, "Target path missing in request")
	}

	if req.GetVolumeCapability() == nil {
		return NodeStageVolumeError(ctx, codes.InvalidArgument, "Error occured, volume Capability missing in request")
	}

	if req.GetVolumeCapability().GetBlock() != nil {
		return NodeStageVolumeError(ctx, codes.InvalidArgument, "Block accessType is unsupported")
	}
	result = "SUCCESS"
	return &csi.NodeStageVolumeResponse{}, nil
}

func NodeUnstageVolumeError(ctx context.Context, errorCode codes.Code, errorMessage string) (*csi.NodeUnstageVolumeResponse, error) {
	err := status.Error(errorCode, strings.ToLower(errorMessage))
	log.Ctx(ctx).Err(err).CallerSkipFrame(1).Msg("Error unstaging volume")
	return &csi.NodeUnstageVolumeResponse{}, err
}

func (ns *NodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	op := "NodeUnstageVolume"
	result := "FAILURE"
	volumeId := req.GetVolumeId()
	ctx, span := otel.Tracer(TracerName).Start(ctx, op, trace.WithNewRoot())
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)

	logger := log.Ctx(ctx)
	logger.Info().Str("volume_id", volumeId).Msg(">>>> Received request")
	defer func() {
		level := zerolog.InfoLevel
		if result != "SUCCESS" {
			level = zerolog.ErrorLevel
		}
		logger.WithLevel(level).Str("result", result).Msg("<<<< Completed processing request")
	}()

	if len(req.GetStagingTargetPath()) == 0 {
		return NodeUnstageVolumeError(ctx, codes.InvalidArgument, "Target path missing in request")
	}
	result = "SUCCESS"
	return &csi.NodeUnstageVolumeResponse{}, nil
}

//goland:noinspection GoUnusedParameter
func (ns *NodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	op := "NodeGetInfo"
	result := "SUCCESS"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op, trace.WithNewRoot())
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
