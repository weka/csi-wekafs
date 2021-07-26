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
	"github.com/google/uuid"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"strings"
	"sync"
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
	api            *apiStore
	debugPath      string
	dynamicVolPath string

	csiMode CsiPluginMode
}

var (
	vendorVersion    = "dev"
	ApiNotFoundError = errors.New("could not get API client by cluster guid")
)

// apiStore hashmap of all APIs defined by credentials + endpoints
type apiStore struct {
	sync.Mutex
	apis map[uint32]*apiclient.ApiClient
}

// getByHash returns pointer to existing API if found by hash, or nil
func (api *apiStore) getByHash(key uint32) *apiclient.ApiClient {
	if val, ok := api.apis[key]; ok {
		return val
	}
	return nil
}

func (api *apiStore) getByClusterGuid(guid uuid.UUID) (*apiclient.ApiClient, error) {
	for _, val := range api.apis {
		if val.ClusterGuid == guid {
			return val, nil
		}
	}
	glog.Errorln("Could not fetch API client for cluster GUID", guid.String())
	return nil, ApiNotFoundError
}

// fromSecrets returns a pointer to API by secret contents
func (api *apiStore) fromSecrets(secrets map[string]string) (*apiclient.ApiClient, error) {
	username := secrets["username"]
	password := secrets["password"]
	organization := secrets["organization"]
	endpointsRaw := secrets["endpoints"]
	endpoints := strings.Split(string(endpointsRaw), ",")
	scheme := secrets["scheme"]
	return api.fromParams(username, password, organization, scheme, endpoints)
}

// fromParams returns a pointer to API by credentials and endpoints
// If this is a new API, it will be created and put in hashmap
func (api *apiStore) fromParams(Username, Password, Organization, Scheme string, Endpoints []string) (*apiclient.ApiClient, error) {
	// doing this to fetch a client hash
	newClient, err := (&apiclient.ApiClient{}).New(Username, Password, Organization, Endpoints, Scheme)
	if err != nil {
		return nil, errors.New("could not create API client object from supplied params")
	}
	hash := newClient.Hash()

	if existingApi := api.getByHash(hash); existingApi != nil {
		glog.V(4).Infoln("Found an existing Weka API client", newClient.Username, "@", strings.Join(newClient.Endpoints, ","))
		return existingApi, nil
	}
	api.Lock()
	defer api.Unlock()
	glog.V(4).Infoln("Creating new Weka API client", newClient.Username, "@", strings.Join(newClient.Endpoints, ","))
	if api.getByHash(hash) != nil {
		return api.getByHash(hash), nil
	}
	api.apis[hash] = newClient
	return newClient, nil
}

func (api *apiStore) GetClientFromSecrets(secrets map[string]string) (*apiclient.ApiClient, error) {
	if len(secrets) > 0 {
		client, err := api.fromSecrets(secrets)
		if err != nil {
			glog.V(4).Infof("API service was not found for request, switching to legacy mode")
		} else {
			glog.V(4).Infof("Successfully initialized API backend for request")
			if err := client.Init(); err != nil {
				glog.Errorln("Failed to initialize API client", client.Username, "@", client.Endpoints)
				return nil, err
			}
		}
	} else {
		glog.V(4).Infof("No API service for request, switching to legacy mode")
	}
	return nil, nil
}

func NewApiStore() *apiStore {
	return &apiStore{
		Mutex: sync.Mutex{},
		apis:  make(map[uint32]*apiclient.ApiClient),
	}
}

func NewWekaFsDriver(driverName, nodeID, endpoint string, maxVolumesPerNode int64, version string, debugPath string, dynmamicVolPath string, csiMode CsiPluginMode) (*wekaFsDriver, error) {
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
		api:               NewApiStore(),
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
		driver.cs = NewControllerServer(driver.nodeID, driver.api, mounter, gc, driver.dynamicVolPath)
	} else {
		driver.cs = &controllerServer{}
	}

	if driver.csiMode == CsiModeNode || driver.csiMode == CsiModeAll {

		// bring up node part
		glog.Infof("Loading NodeServer")
		driver.ns = NewNodeServer(driver.nodeID, driver.maxVolumesPerNode, driver.api, mounter, gc)
	} else {
		driver.ns = &nodeServer{}
	}

	s := NewNonBlockingGRPCServer(driver.csiMode)
	s.Start(driver.endpoint, driver.ids, driver.cs, driver.ns)
	s.Wait()
}

const (
	VolumeTypeDirV1 = "dir/v1"
)

type CsiPluginMode string

const CsiModeNode CsiPluginMode = "node"
const CsiModeController CsiPluginMode = "controller"
const CsiModeAll CsiPluginMode = "all"

func GetCsiPluginMode(mode *string) CsiPluginMode {
	ret := CsiPluginMode(*mode)
	switch ret {
	case CsiModeNode,
		CsiModeController,
		CsiModeAll:
		return ret
	default:
		glog.Fatalln("Unsupported plugin mode", ret)
		return ""
	}
}
