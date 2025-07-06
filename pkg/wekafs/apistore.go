package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"strings"
	"sync"
)

// ApiStore hashmap of all APIs defined by credentials + endpoints
type ApiStore struct {
	sync.Mutex
	apis     map[uint32]*apiclient.ApiClient
	config   *DriverConfig
	Hostname string
	locks    sync.Map // map[uint32]*sync.Mutex
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
	log.Error().Str("cluster_guid", guid.String()).Msg("Could not fetch API client for cluster GUID")
	return nil, ClusterApiNotFoundError
}

// fromSecrets returns a pointer to API by secret contents
func (api *ApiStore) fromSecrets(ctx context.Context, secrets map[string]string, hostname string) (*apiclient.ApiClient, error) {
	endpointsRaw := strings.TrimSpace(strings.ReplaceAll(strings.TrimSuffix(secrets["endpoints"], "\n"), "\n", ","))
	if endpointsRaw == "" {
		return nil, errors.New("no valid endpoints defined in secret, cannot create API client")
	}
	endpoints := func() []string {
		var ret []string
		for _, s := range strings.Split(endpointsRaw, ",") {
			ret = append(ret, strings.TrimSpace(strings.TrimSuffix(s, "\n")))
		}
		return ret
	}()

	var nfsTargetIps []string
	if _, ok := secrets["nfsTargetIps"]; ok {
		nfsTargetIpsRaw := strings.TrimSpace(strings.ReplaceAll(strings.TrimSuffix(secrets["nfsTargetIps"], "\n"), "\n", ","))
		nfsTargetIps = func() []string {
			var ret []string
			if nfsTargetIpsRaw == "" {
				return ret
			}
			for _, s := range strings.Split(nfsTargetIpsRaw, ",") {
				ret = append(ret, strings.TrimSpace(strings.TrimSuffix(s, "\n")))
			}
			return ret
		}()
	}

	localContainerName, ok := secrets["localContainerName"]
	if ok {
		localContainerName = strings.TrimSpace(strings.TrimSuffix(localContainerName, "\n"))
	} else {
		localContainerName = ""
	}
	autoUpdateEndpoints := false
	autoUpdateEndpointsStr, ok := secrets["autoUpdateEndpoints"]
	if ok {
		autoUpdateEndpoints = strings.TrimSpace(strings.TrimSuffix(autoUpdateEndpointsStr, "\n")) == "true"
	}
	caCertificate, ok := secrets["caCertificate"]
	if !ok {
		caCertificate = ""
	}

	preexistingVaultCreds := apiclient.KmsVaultCredentials{}

	kmsVaultNamespaceForFilesystemEncryption, ok := secrets["kmsVaultNamespaceForFilesystemEncryption"]
	if ok {
		preexistingVaultCreds.Namespace = strings.TrimSpace(strings.TrimSuffix(kmsVaultNamespaceForFilesystemEncryption, "\n"))
	}

	kmsVaultKeyIdentifierForFilesystemEncryption, ok := secrets["kmsVaultKeyIdentifierForFilesystemEncryption"]
	if ok {
		preexistingVaultCreds.KeyIdentifier = strings.TrimSpace(strings.TrimSuffix(kmsVaultKeyIdentifierForFilesystemEncryption, "\n"))
	}

	kmsVaultRoleIdForFilesystemEncryption, ok := secrets["kmsVaultRoleIdForFilesystemEncryption"]
	if ok {
		preexistingVaultCreds.RoleId = strings.TrimSpace(strings.TrimSuffix(kmsVaultRoleIdForFilesystemEncryption, "\n"))
	}

	kmsVaultSecretIdForFilesystemEncryption, ok := secrets["kmsVaultSecretIdForFilesystemEncryption"]
	if ok {
		preexistingVaultCreds.SecretId = strings.TrimSpace(strings.TrimSuffix(kmsVaultSecretIdForFilesystemEncryption, "\n"))
	}

	credentials := apiclient.Credentials{
		Username:            strings.TrimSpace(strings.TrimSuffix(secrets["username"], "\n")),
		Password:            strings.TrimSuffix(secrets["password"], "\n"),
		Organization:        strings.TrimSpace(strings.TrimSuffix(secrets["organization"], "\n")),
		Endpoints:           endpoints,
		HttpScheme:          strings.TrimSpace(strings.TrimSuffix(secrets["scheme"], "\n")),
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
	logger.Trace().Msg("Received request to get an API client from credentials")
	credsHash := credentials.Hash()
	lock := api.getLockForHash(credsHash)
	lock.Lock()
	defer lock.Unlock()

	newClient, err := apiclient.NewApiClient(ctx, credentials, api.config.allowInsecureHttps, hostname, api.config.GetDriver().name)
	if err != nil {
		return nil, errors.New("could not create API client object from supplied params")
	}
	hash := newClient.Hash()

	if existingApi := api.getByHash(hash); existingApi != nil {
		logger.Trace().Str("api_client", credentials.String()).Msg("Found an existing Weka API client")
		return existingApi, nil
	}
	api.Lock()
	if api.getByHash(hash) != nil {
		api.Unlock()
		return api.getByHash(hash), nil
	}
	api.Unlock()
	if err := newClient.Init(ctx); err != nil {
		logger.Error().Err(err).Msg("Failed to initialize API client")
		return nil, err
	}
	if !newClient.SupportsAuthenticatedMounts() && credentials.Organization != apiclient.RootOrganizationName {
		return nil, errors.New(fmt.Sprintf(
			"Using Organization %s is not supported on Weka cluster \"%s\".\n"+
				"To support organization other than Root please upgrade to version %s or higher",
			credentials.Organization, newClient.ClusterName, apiclient.MinimumSupportedWekaVersions.MountFilesystemsUsingAuthToken))
	}
	if api.config.allowNfsFailback || api.config.useNfs {
		newClient.NfsInterfaceGroupName = api.config.interfaceGroupName
		newClient.NfsClientGroupName = api.config.clientGroupName
		err := newClient.RegisterNfsClientGroup(ctx)
		if err != nil {
			logger.Error().Err(err).Msg("Failed to register NFS client group")
			return nil, err
		}
	}
	api.Lock()
	defer api.Unlock()
	api.apis[hash] = newClient

	return newClient, nil
}

func (api *ApiStore) GetClientFromSecrets(ctx context.Context, secrets map[string]string) (*apiclient.ApiClient, error) {
	logger := log.Ctx(ctx)
	if len(secrets) == 0 {
		logger.Error().Msg("No secrets provided, cannot proceed")
		return nil, errors.New("no secrets provided")
	}
	client, err := api.fromSecrets(ctx, secrets, api.Hostname)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to initialize API client from secret, cannot proceed")
		return nil, err
	}
	logger.Trace().Msg("Successfully initialized API backend for request")
	return client, nil
}

func NewApiStore(config *DriverConfig, hostname string) *ApiStore {
	s := &ApiStore{
		Mutex:    sync.Mutex{},
		apis:     make(map[uint32]*apiclient.ApiClient),
		config:   config,
		Hostname: hostname,
	}
	return s
}
