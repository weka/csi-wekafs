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
	"errors"
	"fmt"
	"github.com/golang/glog"
	"github.com/google/uuid"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"io/fs"
	"io/ioutil"
	"os"
	"strings"
	"sync"
)

var DefaultVolumePermissions fs.FileMode = 0750

type WekaFsDriver struct {
	name              string
	nodeID            string
	version           string
	endpoint          string
	maxVolumesPerNode int64
	mountMode         string
	mockMount         bool

	ids            *identityServer
	ns             *NodeServer
	cs             *ControllerServer
	api            *ApiStore
	debugPath      string
	dynamicVolPath string

	csiMode        CsiPluginMode
	selinuxSupport bool

	newVolumePrefix              string
	newSnapshotPrefix            string
	allowAutoFsCreation          bool
	allowAutoFsExpansion         bool
	supportSnapshotCapability    bool
	supportVolumeCloneCapability bool
}

type VolumeType string

var (
	vendorVersion           = "dev"
	ClusterApiNotFoundError = errors.New("could not get API client by cluster guid")
)

// ApiStore hashmap of all APIs defined by credentials + endpoints
type ApiStore struct {
	sync.Mutex
	apis          map[uint32]*apiclient.ApiClient
	legacySecrets *map[string]string
}

// Die used to intentionally panic and exit, while updating termination log
func Die(exitMsg string) {
	_ = ioutil.WriteFile("/dev/termination-log", []byte(exitMsg), 0644)
	panic(exitMsg)
}

// getByHash returns pointer to existing API if found by hash, or nil
func (api *ApiStore) getByHash(key uint32) *apiclient.ApiClient {
	if val, ok := api.apis[key]; ok {
		return val
	}
	return nil
}

func (api *ApiStore) getByClusterGuid(guid uuid.UUID) (*apiclient.ApiClient, error) {
	for _, val := range api.apis {
		if val.ClusterGuid == guid {
			return val, nil
		}
	}
	glog.Errorln("Could not fetch API client for cluster GUID", guid.String())
	return nil, ClusterApiNotFoundError
}

// fromSecrets returns a pointer to API by secret contents
func (api *ApiStore) fromSecrets(secrets map[string]string) (*apiclient.ApiClient, error) {
	endpointsRaw := strings.TrimSpace(secrets["endpoints"])
	endpoints := func() []string {
		var ret []string
		for _, s := range strings.Split(string(endpointsRaw), ",") {
			ret = append(ret, strings.TrimSpace(strings.TrimSuffix(s, "\n")))
		}
		return ret
	}()
	credentials := apiclient.Credentials{
		Username:     strings.TrimSpace(strings.TrimSuffix(secrets["username"], "\n")),
		Password:     strings.TrimSuffix(secrets["password"], "\n"),
		Organization: strings.TrimSpace(strings.TrimSuffix(secrets["organization"], "\n")),
		Endpoints:    endpoints,
		HttpScheme:   strings.TrimSpace(strings.TrimSuffix(secrets["scheme"], "\n")),
	}
	return api.fromCredentials(credentials)
}

// fromCredentials returns a pointer to API by credentials and endpoints
// If this is a new API, it will be created and put in hashmap
func (api *ApiStore) fromCredentials(credentials apiclient.Credentials) (*apiclient.ApiClient, error) {
	// doing this to fetch a client hash
	newClient, err := apiclient.NewApiClient(credentials)
	if err != nil {
		return nil, errors.New("could not create API client object from supplied params")
	}
	hash := newClient.Hash()

	if existingApi := api.getByHash(hash); existingApi != nil {
		glog.V(4).Infoln("Found an existing Weka API client", credentials.String())
		return existingApi, nil
	}
	api.Lock()
	defer api.Unlock()
	glog.V(4).Infoln("Creating new Weka API client", credentials.String())
	if api.getByHash(hash) != nil {
		return api.getByHash(hash), nil
	}
	api.apis[hash] = newClient
	if !newClient.SupportsAuthenticatedMounts() && credentials.Organization != apiclient.RootOrganizationName {
		return nil, errors.New(fmt.Sprintf(
			"Using Organization %s is not supported on Weka cluster \"%s\".\n"+
				"To support organization other than Root please upgrade to version %s or higher",
			credentials.Organization, newClient.ClusterName, apiclient.MinimumSupportedWekaVersions.MountFilesystemsUsingAuthToken))
	}
	return newClient, nil
}

func (api *ApiStore) GetDefaultSecrets() (*map[string]string, error) {
	err := pathIsDirectory(LegacySecretPath)
	if err != nil {
		return nil, errors.New("no legacy secret exists")
	}
	KEYS := []string{"scheme", "endpoints", "organization", "username", "password"}
	ret := make(map[string]string)
	for _, k := range KEYS {
		filePath := fmt.Sprintf("%s/%s", LegacySecretPath, k)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return nil, errors.New(fmt.Sprintf("Missing key %s in legacy secret configuration", k))
		}
		contents, err := ioutil.ReadFile(filePath)
		if err != nil {
			return nil, errors.New(fmt.Sprintf("Could not read key %s from legacy secret configuration", k))
		}
		ret[k] = string(contents)
	}
	return &ret, nil
}

func (api *ApiStore) GetClientFromSecrets(ctx context.Context, secrets map[string]string) (*apiclient.ApiClient, error) {
	if len(secrets) == 0 {
		if api.legacySecrets != nil {
			glog.V(4).Infof("No explicit API service for request, using legacySecrets")
			secrets = *api.legacySecrets
		} else {
			glog.V(4).Infof("No API service for request, switching to legacy mode")
			return nil, nil
		}
	}
	client, err := api.fromSecrets(secrets)
	if err != nil || client == nil {
		glog.V(4).Infof("API service was not found for request, switching to legacy mode")
		return nil, nil
	}
	if err := client.Init(ctx); err != nil {
		glog.Errorln("Failed to initialize API client", client.Credentials.String(), err)
		return nil, err
	}
	glog.V(4).Infof("Successfully initialized API backend for request")
	return client, nil
}

func NewApiStore() *ApiStore {
	s := &ApiStore{
		Mutex: sync.Mutex{},
		apis:  make(map[uint32]*apiclient.ApiClient),
	}
	secrets, err := s.GetDefaultSecrets()
	if err != nil {
		glog.V(2).Infoln("No legacy API secrets could be found:", err)
	} else {
		glog.V(2).Infoln("Initialized legacy API secrets")
		s.legacySecrets = secrets
	}
	return s
}

func NewWekaFsDriver(
	driverName, nodeID, endpoint string, maxVolumesPerNode int64, version, debugPath string,
	dynmamicVolPath string, csiMode CsiPluginMode, selinuxSupport bool,
	newVolumePrefix, newSnapshotPrefix string,
	allowAutoFsCreation, allowAutoFsExpansion, removeSnapshotCapability, removeVolumeCloneCapability bool) (*WekaFsDriver, error) {
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

	return &WekaFsDriver{
		name:                         driverName,
		version:                      vendorVersion,
		nodeID:                       nodeID,
		endpoint:                     endpoint,
		maxVolumesPerNode:            maxVolumesPerNode,
		debugPath:                    debugPath,
		dynamicVolPath:               dynmamicVolPath,
		csiMode:                      csiMode, // either "controller", "node", "all"
		api:                          NewApiStore(),
		selinuxSupport:               selinuxSupport,
		newVolumePrefix:              newVolumePrefix,
		newSnapshotPrefix:            newSnapshotPrefix,
		allowAutoFsCreation:          allowAutoFsCreation,
		allowAutoFsExpansion:         allowAutoFsExpansion,
		supportSnapshotCapability:    !removeSnapshotCapability,
		supportVolumeCloneCapability: !removeVolumeCloneCapability,
	}, nil
}

func (driver *WekaFsDriver) Run() {
	// Create GRPC servers
	mounter := &wekaMounter{mountMap: mountsMap{}, debugPath: driver.debugPath, selinuxSupport: driver.selinuxSupport}
	mounter.gc = initInnerPathVolumeGc(mounter)
	// identity server runs always
	glog.Info("Loading IdentityServer")
	driver.ids = NewIdentityServer(driver.name, driver.version)

	if driver.csiMode == CsiModeController || driver.csiMode == CsiModeAll {
		glog.Infof("Loading ControllerServer")
		// bring up controller part
		driver.cs = NewControllerServer(driver.nodeID, driver.api, mounter, driver.dynamicVolPath,
			driver.newVolumePrefix, driver.newSnapshotPrefix, driver.allowAutoFsCreation, driver.allowAutoFsExpansion,
			driver.supportSnapshotCapability, driver.supportVolumeCloneCapability)
	} else {
		driver.cs = &ControllerServer{}
	}

	if driver.csiMode == CsiModeNode || driver.csiMode == CsiModeAll {

		// bring up node part
		glog.Infof("Loading NodeServer")
		driver.ns = NewNodeServer(driver.nodeID, driver.maxVolumesPerNode, driver.api, mounter)
	} else {
		driver.ns = &NodeServer{}
	}

	s := NewNonBlockingGRPCServer(driver.csiMode)
	s.Start(driver.endpoint, driver.ids, driver.cs, driver.ns)
	s.Wait()
}

type CsiPluginMode string

const (
	VolumeTypeDirV1       VolumeType = "dir/v1"      // if specified in storage class, create directory quotas (as in legacy CSI volumes). FS name must be set in SC as well
	VolumeTypeFsV1        VolumeType = "fs/v1"       // if specified in storage class, or volumeType is not specified at all - we will create filesystems
	VolumeTypeFsSnapV1    VolumeType = "snap/v1"     // if specified in storage class, create snapshots of a filesystem, name of the FS must be set in SC as well
	VolumeTypeUnified     VolumeType = "weka/v1"     // no need to specify this
	VolumeTypeUnifiedSnap VolumeType = "wekasnap/v1" // no need to specify this
	VolumeTypeNone        VolumeType = ""
	VolumeTypeUNKNOWN     VolumeType = "AMBIGUOUS_VOLUME_TYPE"

	LegacySecretPath = "/legacy-volume-access"

	CsiModeNode       CsiPluginMode = "node"
	CsiModeController CsiPluginMode = "controller"
	CsiModeAll        CsiPluginMode = "all"
)

var KnownVolTypes = [...]VolumeType{VolumeTypeDirV1, VolumeTypeFsV1, VolumeTypeFsSnapV1, VolumeTypeUnified}

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
