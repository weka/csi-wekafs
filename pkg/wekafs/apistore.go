package wekafs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ApiStore hashmap of all APIs defined by credentials + endpoints
type ApiStore struct {
	sync.RWMutex
	apis     map[uint32]*apiclient.ApiClient
	config   *DriverConfig
	Hostname string
	locks    sync.Map // map[uint32]*sync.Mutex
}

func NewApiStore(config *DriverConfig, hostname string) *ApiStore {
	s := &ApiStore{
		apis:     make(map[uint32]*apiclient.ApiClient),
		config:   config,
		Hostname: hostname,
	}
	return s
}

// getByHash returns pointer to existing API if found by hash, or nil
func (api *ApiStore) getByHash(key uint32) *apiclient.ApiClient {
	api.RLock()
	defer api.RUnlock()
	if val, ok := api.apis[key]; ok {
		return val
	}
	return nil
}

func (api *ApiStore) getByClusterGuid(guid uuid.UUID) (*apiclient.ApiClient, error) {
	api.RLock()
	defer api.RUnlock()
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
	endpointsRaw, ok := secrets["endpoints"]
	if !ok {
		return nil, fmt.Errorf("no endpointsRaw found in secret")
	}
	endpointsRaw = strings.ReplaceAll(trimValue(endpointsRaw), "\n", ",")
	if endpointsRaw == "" {
		return nil, status.Errorf(codes.NotFound, "no valid endpoints defined in secret, cannot create API client")
	}
	endpoints, err := getEndpointsFromRaw(endpointsRaw)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "invalid endpoints defined in secret: %v", err)
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
		return nil, fmt.Errorf("no username found in secret")
	}
	password, ok := secrets["password"]
	if !ok {
		return nil, fmt.Errorf("no password found in secret")
	}
	organization, ok := secrets["organization"]
	if !ok {
		return nil, fmt.Errorf("no organization found in secret")
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
	logger.Trace().Msg("Received request to get an API client from credentials")
	credsHash := credentials.Hash()
	lock := api.getLockForHash(credsHash)
	lock.Lock()
	defer lock.Unlock()

	logger.Debug().Msg("Looking for API client from creds")
	if existingApi := api.getByHash(credsHash); existingApi != nil {
		return existingApi, nil
	}

	newClient, err := apiclient.NewApiClient(ctx, credentials, apiclient.ApiClientOptions{
		AllowInsecureHttps: api.config.allowInsecureHttps,
		Hostname:           hostname,
		DriverName:         api.config.GetDriver().name,
		ApiTimeout:         apiclient.ApiHttpTimeOutSeconds * time.Second,
	})
	if err != nil {
		logger.Error().Err(err).Msg("Failed to create API client from credentials")
		return nil, err
	}

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
	api.apis[credsHash] = newClient

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

func (api *ApiStore) getLockForHash(hash uint32) *sync.Mutex {
	lockIface, _ := api.locks.LoadOrStore(hash, &sync.Mutex{})
	return lockIface.(*sync.Mutex)
}
