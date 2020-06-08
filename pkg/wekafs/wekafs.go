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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	utilexec "k8s.io/utils/exec"
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
	ephemeral         bool
	maxVolumesPerNode int64
	mountMode         string

	ids *identityServer
	ns  *nodeServer
	cs  *controllerServer
}

type wekaFsVolume struct {
	VolName        string `json:"volName"`
	VolID          string `json:"volID"`
	VolSize        int64  `json:"volSize"`
	VolPath        string `json:"volPath"`
	ParentVolID    string `json:"parentVolID,omitempty"`
	ParentSnapID   string `json:"parentSnapID,omitempty"`
	Ephemeral      bool   `json:"ephemeral"`
	FilesystemName string `json:"filesystemName"`
	DirQuotaName   string `json:"dirQuotaName,omitempty"`
	VolumeType     string `json:"volumeType"`
}

var (
	vendorVersion = "dev"
)

const (
	dataRoot = "/wekafs"
)

func init() {

}

func NewWekaFsDriver(driverName, nodeID, endpoint string, ephemeral bool, maxVolumesPerNode int64, version string) (*wekaFsDriver, error) {
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
		ephemeral:         ephemeral,
		maxVolumesPerNode: maxVolumesPerNode,
	}, nil
}

func (hp *wekaFsDriver) Run() {
	// Create GRPC servers
	hp.ids = NewIdentityServer(hp.name, hp.version)
	hp.ns = NewNodeServer(hp.nodeID, hp.ephemeral, hp.maxVolumesPerNode)
	hp.cs = NewControllerServer(hp.ephemeral, hp.nodeID)

	//discoverExistingSnapshots()
	s := NewNonBlockingGRPCServer()
	s.Start(hp.endpoint, hp.ids, hp.cs, hp.ns)
	s.Wait()
}
func DirectoryExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return info.IsDir()
}

// loadFromVolume populates the given destPath with data from the srcVolumeID
func loadFromVolume(size int64, srcVolumeId, destPath string) error {
	srcVolume, err := getVolumeByID(srcVolumeId)
	if err != nil {
		return status.Error(codes.NotFound, "source volumeId does not exist, are source/destination in the same storage class?")
	}
	if srcVolume.VolSize > size {
		return status.Errorf(codes.InvalidArgument, "volume %v size %v is greater than requested volume size %v", srcVolumeId, srcVolume.VolSize, size)
	}

	srcPath := srcVolume.VolPath
	isEmpty, err := VolumeIsEmpty(srcPath)
	if err != nil {
		return status.Errorf(codes.Internal, "failed verification check of source wekafs_dirqouta"+
			" srcVolume %v: %v", srcVolume.VolID, err)
	}

	// If the srcVolume is empty it's a noop and we just move along, otherwise the cp call will fail with a a file stat error DNE
	if !isEmpty {
		args := []string{"-a", srcPath + "/.", destPath + "/"}
		executor := utilexec.New()
		out, err := executor.Command("cp", args...).CombinedOutput()
		if err != nil {
			return status.Errorf(codes.Internal, "failed pre-populate data from srcVolume %v: %v: %s", srcVolume.VolID, err, out)
		}
	}
	return nil
}

