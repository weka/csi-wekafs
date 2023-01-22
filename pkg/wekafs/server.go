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
	"google.golang.org/grpc"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

const (
	xattrCapacity   = "user.weka_capacity"
	xattrVolumeName = "user.weka_k8s_volname"
)

func NewNonBlockingGRPCServer(mode CsiPluginMode) *nonBlockingGRPCServer {
	return &nonBlockingGRPCServer{
		csiMmode: mode,
	}
}

// NonBlocking server
type nonBlockingGRPCServer struct {
	wg       sync.WaitGroup
	server   *grpc.Server
	csiMmode CsiPluginMode
}

func (s *nonBlockingGRPCServer) Start(endpoint string, ids csi.IdentityServer, cs csi.ControllerServer, ns csi.NodeServer) {

	s.wg.Add(1)

	go s.serve(endpoint, ids, cs, ns)

	return
}

func (s *nonBlockingGRPCServer) Wait() {
	s.wg.Wait()
}

func (s *nonBlockingGRPCServer) Stop() {
	s.server.GracefulStop()
}

func (s *nonBlockingGRPCServer) ForceStop() {
	s.server.Stop()
}

func (s *nonBlockingGRPCServer) serve(endpoint string, ids csi.IdentityServer, cs csi.ControllerServer, ns csi.NodeServer) {

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

	listener, err := net.Listen(proto, addr)
	if err != nil {
		Die(fmt.Sprintf("Failed to listen: %v", err.Error()))
	}

	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(logGRPC),
	}
	server := grpc.NewServer(opts...)
	s.server = server

	if ids != nil {
		log.Info().Msg("Registering GRPC IdentityServer")
		csi.RegisterIdentityServer(server, ids)
	}
	if s.csiMmode == CsiModeController || s.csiMmode == CsiModeAll {
		if cs != nil {
			log.Info().Msg("Registering GRPC ControllerServer")
			csi.RegisterControllerServer(server, cs)
		}
	}
	if s.csiMmode == CsiModeNode || s.csiMmode == CsiModeAll {
		if ns != nil {
			log.Info().Msg("Registering GRPC NodeServer")
			csi.RegisterNodeServer(server, ns)
		}
	}

	log.Info().Str("address", listener.Addr().String()).Msg("Listening for connections on UNIX socket")

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
	ctx = log.With().Logger().WithContext(ctx)
	if info.FullMethod != "/csi.v1.Identity/Probe" {
		// suppress annoying probe messages
		log.Ctx(ctx).Trace().Str("method", info.FullMethod).Str("request", protosanitizer.StripSecrets(req).String()).Msg("GRPC request")
	}
	resp, err := handler(ctx, req)
	if err != nil {
		log.Ctx(ctx).Trace().Err(err).Msg("GRPC error")
	} else {
		if info.FullMethod != "/csi.v1.Identity/Probe" {
			// suppress annoying probe messages
			log.Ctx(ctx).Trace().Str("response", protosanitizer.StripSecrets(resp).String()).Msg("GRPC response")
		}
	}
	return resp, err
}
