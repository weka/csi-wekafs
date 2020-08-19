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
	"github.com/golang/glog"
)

type wekaFsDriver struct {
	name              string
	nodeID            string
	version           string
	endpoint          string
	maxVolumesPerNode int64
	mountMode         string
	mockMount         bool

	ids            *identityServer
	ns             *nodeServer
	cs             *controllerServer
	debugPath      string
	dynamicVolPath string

	csiMode CsiModeType
}

var (
	vendorVersion = "dev"
)

type CsiModeType string

func NewWekaFsDriver(driverName, nodeID, endpoint string, maxVolumesPerNode int64, version string, debugPath string, dynmamicVolPath string, csiMode CsiModeType) (*wekaFsDriver, error) {
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

	glog.Infof("Driver: %v ", driverName)
	glog.Infof("Version: %s", vendorVersion)

	glog.Infof("csiMode: %s", csiMode)

	return &wekaFsDriver{
		name:              driverName,
		version:           vendorVersion,
		nodeID:            nodeID,
		endpoint:          endpoint,
		maxVolumesPerNode: maxVolumesPerNode,
		debugPath:         debugPath,
		dynamicVolPath:    dynmamicVolPath,
		csiMode:           csiMode, // either "controller", "node", "all"
	}, nil
}

func (driver *wekaFsDriver) Run() {
	// Create GRPC servers
	mounter := &wekaMounter{mountMap: mountsMap{}, debugPath: driver.debugPath}
	gc := initDirVolumeGc(mounter)

	// identity server runs always
	glog.Info("Loading IdentityServer")
	driver.ids = NewIdentityServer(driver.name, driver.version)

	if driver.csiMode == CsiModeController || driver.csiMode == CsiModeAll {
		glog.Infof("Loading ControllerServer")
		// bring up controller part
		driver.cs = NewControllerServer(driver.nodeID, mounter, gc, driver.dynamicVolPath)
	} else {
		driver.cs = &controllerServer{}
	}

	if driver.csiMode == CsiModeNode || driver.csiMode == CsiModeAll {

		// bring up node part
		glog.Infof("Loading NodeServer")
		driver.ns = NewNodeServer(driver.nodeID, driver.maxVolumesPerNode, mounter, gc)
	} else {
		driver.ns = &nodeServer{}
	}

	s := NewNonBlockingGRPCServer(driver.csiMode)
	s.Start(driver.endpoint, driver.ids, driver.cs, driver.ns)
	s.Wait()
}

const (
	VolumeTypeDirV1 = "dir/v1"
	CsiModeNode = "node"
	CsiModeController = "controller"
	CsiModeAll = "all"
)
