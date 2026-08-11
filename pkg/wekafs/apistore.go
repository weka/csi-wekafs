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
	"os"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

var ClusterApiNotFoundError = errors.New("could not get API client by cluster guid")

// ApiStore hashmap of all APIs defined by credentials + endpoints
type ApiStore struct {
	sync.RWMutex
	apis          map[uint32]*apiclient.ApiClient
	legacySecrets *map[string]string
	config        *DriverConfig
	Hostname      string
}

// getByHash returns pointer to existing API if found by hash, or nil
func (api *ApiStore) getByHash(key uint32) *apiclient.ApiClient {
	api.RLock()
	defer api.RUnlock()
	return api.getByHashLocked(key)
}

// getByHashLocked is getByHash for callers that already hold the lock, since Go's RWMutex is not
// reentrant and taking it again here would deadlock.
// REQUIRES: api's read or write lock is held by the caller.
func (api *ApiStore) getByHashLocked(key uint32) *apiclient.ApiClient {
	if val, ok := api.apis[key]; ok {
		return val
	}
	return nil
}

func (api *ApiStore) getByClusterGuid(guid uuid.UUID) (*apiclient.ApiClient, error) {
	api.RLock()
	defer api.RUnlock()
	for _, val := range api.apis {
		if val.GetClusterGuid() == guid {
			return val, nil
		}
	}
	log.Error().Str("cluster_guid", guid.String()).Msg("Could not fetch API client for cluster GUID")
	return nil, ClusterApiNotFoundError
}

// fromSecrets returns a pointer to API by secret contents
func (api *ApiStore) fromSecrets(ctx context.Context, secrets map[string]string, hostname string) (*apiclient.ApiClient, error) {
	endpointsRawValue, ok := secrets["endpoints"]
	if !ok {
		return nil, errors.New("no endpoints found in secret")
	}
	endpointsRaw := strings.ReplaceAll(trimValue(endpointsRawValue), "\n", ",")
	if endpointsRaw == "" {
		return nil, status.Errorf(codes.NotFound, "no valid endpoints defined in secret, cannot create API client")
	}
	var endpoints []string
	for _, s := range strings.Split(endpointsRaw, ",") {
		if endpoint := trimValue(s); endpoint != "" {
			endpoints = append(endpoints, endpoint)
		}
	}
	if len(endpoints) == 0 {
		return nil, status.Errorf(codes.NotFound, "invalid endpoints defined in secret: %s", endpointsRawValue)
	}

	var nfsTargetIps []string
	if _, ok := secrets["nfsTargetIps"]; ok {
		nfsTargetIpsRaw := strings.ReplaceAll(trimValue(secrets["nfsTargetIps"]), "\n", ",")
		nfsTargetIps = func() []string {
			var ret []string
			if nfsTargetIpsRaw == "" {
				return ret
			}
			for _, s := range strings.Split(nfsTargetIpsRaw, ",") {
				ret = append(ret, trimValue(s))
			}
			return ret
		}()
	}

	localContainerName, ok := secrets["localContainerName"]
	if ok {
		localContainerName = trimValue(localContainerName)
	} else {
		localContainerName = ""
	}
	autoUpdateEndpoints := false
	autoUpdateEndpointsStr, ok := secrets["autoUpdateEndpoints"]
	if ok {
		autoUpdateEndpoints = trimValue(autoUpdateEndpointsStr) == "true"
	}
	caCertificate, ok := secrets["caCertificate"]
	if !ok {
		caCertificate = ""
	}

	preexistingVaultCreds := apiclient.KmsVaultCredentials{}

	kmsVaultNamespaceForFilesystemEncryption, ok := secrets["kmsVaultNamespaceForFilesystemEncryption"]
	if ok {
		preexistingVaultCreds.Namespace = trimValue(kmsVaultNamespaceForFilesystemEncryption)
	}

	kmsVaultKeyIdentifierForFilesystemEncryption, ok := secrets["kmsVaultKeyIdentifierForFilesystemEncryption"]
	if ok {
		preexistingVaultCreds.KeyIdentifier = trimValue(kmsVaultKeyIdentifierForFilesystemEncryption)
	}

	kmsVaultRoleIdForFilesystemEncryption, ok := secrets["kmsVaultRoleIdForFilesystemEncryption"]
	if ok {
		preexistingVaultCreds.RoleId = trimValue(kmsVaultRoleIdForFilesystemEncryption)
	}

	kmsVaultSecretIdForFilesystemEncryption, ok := secrets["kmsVaultSecretIdForFilesystemEncryption"]
	if ok {
		preexistingVaultCreds.SecretId = trimValue(kmsVaultSecretIdForFilesystemEncryption)
	}

	username, ok := secrets["username"]
	if !ok {
		return nil, errors.New("no username found in secret")
	}
	password, ok := secrets["password"]
	if !ok {
		return nil, errors.New("no password found in secret")
	}
	organization, ok := secrets["organization"]
	if !ok {
		return nil, errors.New("no organization found in secret")
	}

	credentials := apiclient.Credentials{
		Username:            trimValue(username),
		Password:            strings.TrimSuffix(password, "\n"),
		Organization:        trimValue(organization),
		Endpoints:           endpoints,
		HttpScheme:          trimValue(secrets["scheme"]),
		LocalContainerName:  localContainerName,
		AutoUpdateEndpoints: autoUpdateEndpoints,
		CaCertificate:       caCertificate,
		NfsTargetIPs:        nfsTargetIps,
		KmsPreexistingCredentialsForVolumeEncryption: preexistingVaultCreds,
	}
	return api.fromCredentials(ctx, credentials, hostname)
}

// fromCredentials returns a pointer to API by credentials and endpoints
// If this is a new API, it will be created and put in hashmap
func (api *ApiStore) fromCredentials(ctx context.Context, credentials apiclient.Credentials, hostname string) (*apiclient.ApiClient, error) {
	logger := log.Ctx(ctx)
	logger.Trace().Str("api_client", credentials.String()).Msg("Creating new Weka API client")
	// doing this to fetch a client hash
	newClient, err := apiclient.NewApiClient(ctx, credentials, apiclient.ApiClientOptions{
		AllowInsecureHttps: api.config.allowInsecureHttps,
		Hostname:           hostname,
	})
	if err != nil {
		return nil, errors.New("could not create API client object from supplied params")
	}
	hash := newClient.Hash()

	if existingApi := api.getByHash(hash); existingApi != nil {
		logger.Trace().Str("api_client", credentials.String()).Msg("Found an existing Weka API client")
		return existingApi, nil
	}
	api.Lock()
	defer api.Unlock()
	if existingApi := api.getByHashLocked(hash); existingApi != nil {
		return existingApi, nil
	}
	if err := newClient.Init(ctx); err != nil {
		logger.Error().Err(err).Msg("Failed to initialize API client")
		return nil, err
	}
	if !newClient.SupportsAuthenticatedMounts() && credentials.Organization != apiclient.RootOrganizationName {
		return nil, errors.New(fmt.Sprintf(
			"Using Organization %s is not supported on Weka cluster \"%s\".\n"+
				"To support organization other than Root please upgrade to version %s or higher",
			credentials.Organization, newClient.GetClusterName(), apiclient.MinimumSupportedWekaVersions.MountFilesystemsUsingAuthToken))
	}
	if (api.config.allowNfsFailback || api.config.useNfs) && !api.config.isInDevMode() {
		newClient.NfsInterfaceGroupName = api.config.interfaceGroupName
		newClient.NfsClientGroupName = api.config.clientGroupName
		err := newClient.RegisterNfsClientGroup(ctx)
		if err != nil {
			logger.Error().Err(err).Msg("Failed to register NFS client group")
			return nil, err
		}
	}
	api.apis[hash] = newClient

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
		contents, err := os.ReadFile(filePath)
		if err != nil {
			return nil, errors.New(fmt.Sprintf("Could not read key %s from legacy secret configuration", k))
		}
		ret[k] = string(contents)
	}
	return &ret, nil
}

func (api *ApiStore) GetClientFromSecrets(ctx context.Context, secrets map[string]string) (*apiclient.ApiClient, error) {
	logger := log.Ctx(ctx)
	if len(secrets) == 0 {
		if api.legacySecrets != nil {
			logger.Trace().Msg("No explicit API service for request, using legacySecrets")
			secrets = *api.legacySecrets
		} else {
			logger.Trace().Msg("No API service for request, switching to legacy mode")
			return nil, nil
		}
	}
	client, err := api.fromSecrets(ctx, secrets, api.Hostname)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to initialize API client from secret, cannot proceed")
		return nil, err
	}
	if client == nil {
		logger.Trace().Msg("API service was not found for request, switching to legacy mode")
		return nil, nil
	}
	logger.Trace().Msg("Successfully initialized API backend for request")
	return client, nil
}

func NewApiStore(config *DriverConfig, hostname string) *ApiStore {
	s := &ApiStore{
		apis:     make(map[uint32]*apiclient.ApiClient),
		config:   config,
		Hostname: hostname,
	}
	secrets, err := s.GetDefaultSecrets()
	if err != nil {
		log.Trace().Msg("No global API secret defined")
	} else {
		log.Info().Msg("Initialized API with global API secret")
		s.legacySecrets = secrets
	}
	return s
}
