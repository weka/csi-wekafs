/*
Copyright 2019 The Kubernetes Authors.

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
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

//goland:noinspection GoExportedFuncWithUnexportedType
func NewNonBlockingGRPCServer(mode CsiPluginMode, config *DriverConfig) *nonBlockingGRPCServer {
	return &nonBlockingGRPCServer{
		csiMode: mode,
		config:  config,
	}
}

// NonBlocking server
type nonBlockingGRPCServer struct {
	wg      sync.WaitGroup
	server  *grpc.Server
	csiMode CsiPluginMode
	config  *DriverConfig
}

func (s *nonBlockingGRPCServer) Start(endpoint string, ids csi.IdentityServer, cs csi.ControllerServer, ns csi.NodeServer) {
	if s == nil {
		return
	}
	s.wg.Add(1)

	go s.serve(endpoint, ids, cs, ns)

	return
}

func (s *nonBlockingGRPCServer) Wait() {
	s.wg.Wait()
}

func (s *nonBlockingGRPCServer) Stop() {
	if s == nil || s.server == nil {
		return
	}
	s.server.GracefulStop()
}

func (s *nonBlockingGRPCServer) ForceStop() {
	s.server.Stop()
}

func (s *nonBlockingGRPCServer) serve(endpoint string, ids csi.IdentityServer, cs csi.ControllerServer, ns csi.NodeServer) {

	var listener net.Listener
	if s.csiMode != CsiModeMetricsServer {
		proto, addr, err := parseEndpoint(endpoint)
		if err != nil {
			log.Fatal().Err(err)
		}

		if proto == "unix" {
			addr = "/" + addr
			if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
				Die(fmt.Sprintf("Failed to remove %s, error: %s", addr, err.Error()))
			}
		}

		listener, err = net.Listen(proto, addr)
		if err != nil {
			Die(fmt.Sprintf("Failed to listen: %v", err.Error()))
		}
	}
	var maxConcurrentStreams int64
	for _, val := range s.config.maxConcurrencyPerOp {
		maxConcurrentStreams += val
	}

	maxConcurrentStreams *= 2 // add some extra for the liveness etc.
	if maxConcurrentStreams < 256 {
		maxConcurrentStreams = 256
	}

	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(logGRPC),
		grpc.MaxConcurrentStreams(uint32(maxConcurrentStreams)),
	}

	server := grpc.NewServer(opts...)
	s.server = server

	if s.csiMode != CsiModeMetricsServer {
		log.Info().Msg("Registering GRPC IdentityServer")
		csi.RegisterIdentityServer(server, ids)
	}
	if s.csiMode == CsiModeController || s.csiMode == CsiModeAll {
		if cs != nil {
			log.Info().Msg("Registering GRPC ControllerServer")
			csi.RegisterControllerServer(server, cs)
		}
	}
	if s.csiMode == CsiModeNode || s.csiMode == CsiModeAll {
		if ns != nil {
			log.Info().Msg("Registering GRPC NodeServer")
			csi.RegisterNodeServer(server, ns)
		}
	}

	if s.csiMode != CsiModeMetricsServer {
		log.Info().Str("address", listener.Addr().String()).Msg("Listening for connections on UNIX socket")
	}

	if err := server.Serve(listener); err != nil {
		Die(err.Error())
	}

}

func parseEndpoint(ep string) (string, string, error) {
	if strings.HasPrefix(strings.ToLower(ep), "unix://") || strings.HasPrefix(strings.ToLower(ep), "tcp://") {
		s := strings.SplitN(ep, "://", 2)
		if s[1] != "" {
			return s[0], s[1], nil
		}
	}
	return "", "", fmt.Errorf("Invalid endpoint: %v", ep)
}

func logGRPC(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	ctx, span := otel.Tracer(TracerName).Start(ctx, "GrpcRequest")
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	if info.FullMethod != "/csi.v1.Identity/Probe" {
		// suppress annoying probe messages
		logger.Trace().Str("method", info.FullMethod).Str("request", protosanitizer.StripSecrets(req).String()).Msg("GRPC request")
	}
	resp, err := handler(ctx, req)
	if err != nil {
		logger.Trace().Err(err).Msg("GRPC error")
	} else {
		if info.FullMethod != "/csi.v1.Identity/Probe" {
			// suppress annoying probe messages
			logger.Trace().Str("response", protosanitizer.StripSecrets(resp).String()).Msg("GRPC response")
		}
	}
	return resp, err
}
