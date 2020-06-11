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
	"errors"
	"fmt"
	"github.com/golang/glog"
	"os"
)

const (
	kib    int64 = 1024
	mib    int64 = kib * 1024
	gib    int64 = mib * 1024
	gib100 int64 = gib * 100
	tib    int64 = gib * 1024
	tib100 int64 = tib * 100
)

type wekaFsDriver struct {
	name              string
	nodeID            string
	version           string
	endpoint          string
	maxVolumesPerNode int64
	mountMode         string
	mockMount         bool

	ids *identityServer
	ns  *nodeServer
	cs  *controllerServer
}

var (
	vendorVersion = "dev"
)

const (
	dataRoot = "/wekafs"
)

func init() {

}

func NewWekaFsDriver(driverName, nodeID, endpoint string, maxVolumesPerNode int64, version string) (*wekaFsDriver, error) {
	if driverName == "" {
		return nil, errors.New("no driver name provided")
	}

	if nodeID == "" {
		return nil, errors.New("no node id provided")
	}

	if endpoint == "" {
		return nil, errors.New("no driver endpoint provided")
	}
	if version != "" {
		vendorVersion = version
	}

	// Check that the wekafs directory exists
	if _, err := os.Stat(dataRoot); os.IsNotExist(err) {
		return nil, fmt.Errorf("wekafs directory not exists on host, weka may be not installed: %v", err)
	}

	glog.Infof("Driver: %v ", driverName)
	glog.Infof("Version: %s", vendorVersion)

	return &wekaFsDriver{
		name:              driverName,
		version:           vendorVersion,
		nodeID:            nodeID,
		endpoint:          endpoint,
		maxVolumesPerNode: maxVolumesPerNode,
	}, nil
}

func (hp *wekaFsDriver) Run() {
	// Create GRPC servers
	mounter := &wekaMounter{mountMap: mountsMap{}}
	gc := initDirVolumeGc()

	hp.ids = NewIdentityServer(hp.name, hp.version)
	hp.ns = NewNodeServer(hp.nodeID, hp.maxVolumesPerNode, mounter, gc)
	hp.cs = NewControllerServer(hp.nodeID, mounter, gc)

	//discoverExistingSnapshots()
	s := NewNonBlockingGRPCServer()
	s.Start(hp.endpoint, hp.ids, hp.cs, hp.ns)
	s.Wait()
}

const (
	VolumeTypeDirV1 = "dir/v1"
)
