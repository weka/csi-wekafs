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

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type identityServer struct {
	csi.UnimplementedIdentityServer
	name    string
	version string
	config  *DriverConfig
}

//goland:noinspection GoExportedFuncWithUnexportedType
func NewIdentityServer(name, version string, config *DriverConfig) *identityServer {
	return &identityServer{
		name:    name,
		version: version,
		config:  config,
	}
}

//goland:noinspection GoUnusedParameter
func (ids *identityServer) GetPluginInfo(ctx context.Context, req *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	op := "GetPluginInfo"
	result := "SUCCESS"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
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

	if ids.name == "" {
		return nil, status.Error(codes.Unavailable, "Driver name not configured")
	}

	if ids.version == "" {
		return nil, status.Error(codes.Unavailable, "Driver is missing version")
	}
	return &csi.GetPluginInfoResponse{
		Name:          ids.name,
		VendorVersion: ids.version,
	}, nil
}

func (ids *identityServer) getConfig() *DriverConfig {
	return ids.config
}

//goland:noinspection GoUnusedParameter
func (ids *identityServer) Probe(ctx context.Context, req *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	logger := log.Ctx(ctx)
	logger.Trace().Dur("timeout", ids.config.healthProbeWekaTimeout).Msg("CSI Probe: checking Weka client status")
	probeCtx, probeCancel := context.WithTimeout(ctx, ids.config.healthProbeWekaTimeout)
	defer probeCancel()
	config := ids.getConfig()

	// Each transport is judged separately, and the mounters follow. Probe is the only thing that runs
	// repeatedly on a node, so it is where a Weka client appearing or disappearing under a live driver
	// gets noticed: a node that failed back to NFS starts serving wekafs again once its client returns,
	// without a restart. Mounts already made over the other transport are unaffected - they are
	// unmounted through the mounter that made them, whatever is enabled now.
	nfsReady := config.useNfs || config.allowNfsFailback
	wekafsReady := !config.useNfs && isWekaRunning(probeCtx)

	if mounters := config.GetDriver().mounters; mounters != nil {
		setMounterEnabled(mounters.nfs, nfsReady)
		setMounterEnabled(mounters.wekafs, wekafsReady)
	}

	// Readiness is unchanged: the driver can serve as long as either transport can.
	isReady := nfsReady || wekafsReady
	if isReady && !wekafsReady {
		logger.Trace().Msg("CSI Probe: Weka client not running but NFS transport available, reporting ready")
	}
	// manage node topology labels only if set by configuration
	if ids.config.manageNodeTopologyLabels {
		if !isReady {
			logger.Error().Msg("CSI Probe FAILED: Weka driver not running on host and NFS transport is not configured, not ready to perform operations")
			if ids.config.driverRef.csiMode == CsiModeNode {
				ids.getConfig().GetDriver().CleanupNodeLabels(ctx)
			}
		} else {
			ids.getConfig().GetDriver().SetNodeLabels(ctx, wekafsReady)
		}
	}
	logger.Trace().Bool("ready", isReady).Msg("CSI Probe completed")
	return &csi.ProbeResponse{
		Ready: &wrapperspb.BoolValue{
			Value: isReady,
		},
	}, nil
}

//goland:noinspection GoUnusedParameter
func (ids *identityServer) GetPluginCapabilities(ctx context.Context, req *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	op := "GetPluginCapabilities"
	result := "SUCCESS"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
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
	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: []*csi.PluginCapability{
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
					},
				},
			},
			{
				Type: &csi.PluginCapability_VolumeExpansion_{
					VolumeExpansion: &csi.PluginCapability_VolumeExpansion{
						Type: csi.PluginCapability_VolumeExpansion_ONLINE,
					},
				},
			},
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_VOLUME_ACCESSIBILITY_CONSTRAINTS,
					},
				},
			},
		},
	}, nil
}
