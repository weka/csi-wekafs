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
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type identityServer struct {
	csi.UnimplementedIdentityServer
	name    string
	version string
	config  *DriverConfig
}

//goland:noinspection GoExportedFuncWithUnexportedType
func NewIdentityServer(driver *WekaFsDriver) *identityServer {
	if driver == nil {
		panic("Driver is nil")
	}
	return &identityServer{
		name:    driver.name,
		version: driver.version,
		config:  driver.config,
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
	config := ids.getConfig()
	driver := config.GetDriver()
	mounters := driver.mounters

	nfsReady := config.useNfs || config.allowNfsFailback
	// weka is ready if we are in dev mode or weka is running AND NFS is not forced
	wekafsReady := isWekaRunning() && !config.useNfs

	if nfsReady {
		mounters.nfs.Enable()
	} else {
		mounters.nfs.Disable()
	}

	if wekafsReady {
		mounters.wekafs.Enable()
	} else {
		mounters.wekafs.Disable()
	}

	serverReady := nfsReady || wekafsReady

	// manage node topology labels only if set by configuration
	if ids.config.manageNodeTopologyLabels {
		if !serverReady {
			logger.Error().Msg("Weka driver not running on host and NFS transport is not configured, not ready to perform operations")
			if ids.config.driverRef.csiMode == CsiModeNode || ids.config.driverRef.csiMode == CsiModeAll {
				ids.getConfig().GetDriver().CleanupNodeLabels(ctx)
			}
		} else if !ids.getConfig().isInDevMode() {
			ids.getConfig().GetDriver().SetNodeLabels(ctx)
		}
	}

	return &csi.ProbeResponse{
		Ready: &wrapperspb.BoolValue{
			Value: serverReady,
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
