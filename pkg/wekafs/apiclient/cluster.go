package apiclient

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"golang.org/x/exp/slices"
	"k8s.io/apimachinery/pkg/util/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const ApiPathLogin = "login"

const ApiPathTokenExpiry = "security/defaultTokensExpiry"

const ApiPathRefresh = "login/refresh"

const ApiPathClusterInfo = "cluster"

const ApiPathContainersInfo = "containers"

const ApiPathWhoami = "users/whoami"

// updateTokensExpiryInterval fetches the refresh token expiry from API
func (a *ApiClient) updateTokensExpiryInterval(ctx context.Context) error {
	responseData := &TokenExpiryResponse{}
	if err := a.Get(ctx, ApiPathTokenExpiry, nil, responseData); err != nil {
		return err
	}
	a.refreshTokenExpiryInterval = responseData.RefreshTokenExpiry
	a.apiTokenExpiryInterval = responseData.AccessTokenExpiry
	log.Ctx(ctx).Trace().Msg("Updated refresh token validity period")
	return nil
}

// fetchClusterInfo performed each login and checks for version
func (a *ApiClient) fetchClusterInfo(ctx context.Context) error {
	responseData := &ClusterInfoResponse{}
	if err := a.Get(ctx, ApiPathClusterInfo, nil, responseData); err != nil {
		return err
	}
	a.ClusterName = responseData.Name
	a.ClusterGuid = responseData.Guid
	clusterVersion := fmt.Sprintf("v%s", responseData.Release)
	a.CompatibilityMap.fillIn(clusterVersion)
	logger := log.Logger.With().Str("cluster_name", a.ClusterName).Logger()
	logger.Info().Str("cluster_guid", a.ClusterGuid.String()).
		Str("cluster_version", clusterVersion).Msg("Successfully connected to cluster")
	logger.Info().Msg(fmt.Sprintf("Cluster compatibility for filesystem as CSI volume: %t", a.SupportsFilesystemAsVolume()))
	logger.Info().Msg(fmt.Sprintf("Cluster compatibility for quota directory as CSI volume: %t", a.SupportsQuotaDirectoryAsVolume()))
	logger.Info().Msg(fmt.Sprintf("Cluster compatibility for regular directory as CSI volume: %t", a.SupportsDirectoryAsVolume()))
	logger.Info().Msg(fmt.Sprintf("Cluster compatibility for authenticated filesystem mounts: %t", a.SupportsAuthenticatedMounts()))
	logger.Info().Msg(fmt.Sprintf("Cluster compatibility for new filesystem from snapshot: %t", a.SupportsNewFileSystemFromSnapshot()))
	logger.Info().Msg(fmt.Sprintf("Cluster compatibility for cloning filesystems: %t", a.SupportsFilesystemCloning()))
	logger.Info().Msg(fmt.Sprintf("Cluster compatibility for sync_on_close mount option: %t", a.SupportsSyncOnCloseMountOption()))
	logger.Info().Msg(fmt.Sprintf("Cluster compatibility for supporting multiple connections: %t", a.SupportsMultipleClusters()))
	logger.Info().Msg(fmt.Sprintf("Cluster requires using new API path for nodes (nodes->processes): %t", a.RequiresNewNodePath()))
	logger.Info().Msg(fmt.Sprintf("Cluster supports URL query parameters: %t", a.SupportsUrlQueryParams()))
	logger.Info().Msg(fmt.Sprintf("Cluster supports quotas on snapshots: %t", a.SupportsQuotaOnSnapshots()))
	logger.Info().Msg(fmt.Sprintf("Cluster supports encryption without KMS: %t", a.SupportsEncryptionWithNoKms()))
	logger.Info().Msg(fmt.Sprintf("Cluster supports encryption of filesystems with a cluster-wide key: %t", a.SupportsEncryptionWithCommonKey()))
	logger.Info().Msg(fmt.Sprintf("Cluster supports encryption of filesystems with custom keys: %t", a.SupportsCustomEncryptionSettings()))
	logger.Info().Msg(fmt.Sprintf("Cluster supports resolving paths to inodes: %t", a.SupportsResolvePathToInode()))
	return nil
}

func (a *ApiClient) GetFreeCapacity(ctx context.Context) (uint64, error) {
	responseData := &ClusterInfoResponse{}
	if err := a.Get(ctx, ApiPathClusterInfo, nil, responseData); err != nil {
		return 0, err
	}
	capacity := responseData.Capacity.UnprovisionedBytes
	log.Ctx(ctx).Trace().Uint64("free_capacity", capacity).Msg("Obtained cluster free capacity")
	return capacity, nil
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Org      string `json:"org"`
}

type LoginResponse struct {
	AccessToken  string `json:"access_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

func (lr *LoginResponse) CombinePartialResponse(next ApiObjectResponse) error {
	panic("not implemented")
}

func (lr *LoginResponse) SupportsPagination() bool {
	return false
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token,omitempty"`
}

type RefreshResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

func (rr *RefreshResponse) CombinePartialResponse(next ApiObjectResponse) error {
	panic("not implemented")
}

func (rr *RefreshResponse) SupportsPagination() bool {
	return false
}

type TokenExpiryResponse struct {
	AccessTokenExpiry  int64 `json:"access_token_expiry"`
	RefreshTokenExpiry int64 `json:"refresh_token_expiry"`
}

func (ter *TokenExpiryResponse) CombinePartialResponse(next ApiObjectResponse) error {
	panic("not implemented")
}

func (ter *TokenExpiryResponse) SupportsPagination() bool {
	return false
}

type Capacity struct {
	TotalBytes         uint64 `json:"total_bytes"`
	HotSpareBytes      uint64 `json:"hot_spare_bytes"`
	UnprovisionedBytes uint64 `json:"unprovisioned_bytes"`
}

type ClusterInfoResponse struct {
	Name        string    `json:"name"`
	ReleaseHash string    `json:"release_hash"`
	InitStage   string    `json:"init_stage"`
	Release     string    `json:"release"`
	Guid        uuid.UUID `json:"guid"`
	Capacity    Capacity  `json:"capacity,omitempty"`
}

func (cir *ClusterInfoResponse) CombinePartialResponse(next ApiObjectResponse) error {
	panic("not implemented")
}

func (cir *ClusterInfoResponse) SupportsPagination() bool {
	return false
}

type WhoamiResponse struct {
	OrgId    int         `json:"org_id,omitempty"`
	Username string      `json:"username,omitempty"`
	Source   string      `json:"source,omitempty"`
	Uid      uuid.UUID   `json:"uid,omitempty"`
	Role     ApiUserRole `json:"role,omitempty"`
	OrgName  string      `json:"org_name,omitempty"`
}

func (war *WhoamiResponse) CombinePartialResponse(next ApiObjectResponse) error {
	panic("not implemented")
}

func (war *WhoamiResponse) SupportsPagination() bool {
	return false
}

type Container struct {
	Id                   string    `json:"id,omitempty"`
	SwReleaseString      string    `json:"sw_release_string,omitempty"`
	Mode                 string    `json:"mode,omitempty"`
	ContainerName        string    `json:"container_name,omitempty"`
	FailureDomain        string    `json:"failure_domain,omitempty"`
	AddedTime            time.Time `json:"added_time,omitempty"`
	Uid                  string    `json:"uid,omitempty"`
	DrivesDedicatedCores int       `json:"drives_dedicated_cores,omitempty"`
	Hostname             string    `json:"hostname,omitempty"`
	Ips                  []string  `json:"ips,omitempty"`
	MemberOfLeadership   bool      `json:"member_of_leadership,omitempty"`
	Cloud                struct {
		InstanceType     string `json:"instance_type,omitempty"`
		Provider         string `json:"provider,omitempty"`
		AvailabilityZone string `json:"availability_zone,omitempty"`
		InstanceId       string `json:"instance_id,omitempty"`
	} `json:"cloud,omitempty"`
	LastFailureTime interface{} `json:"last_failure_time,omitempty"`
	State           string      `json:"state,omitempty"`
	StartTime       time.Time   `json:"start_time,omitempty"`
	Aws             struct {
		InstanceType     string `json:"instance_type,omitempty"`
		Provider         string `json:"provider,omitempty"`
		AvailabilityZone string `json:"availability_zone,omitempty"`
		InstanceId       string `json:"instance_id,omitempty"`
	} `json:"aws,omitempty"`
	SwVersion string `json:"sw_version,omitempty"`
	OsInfo    struct {
		KernelName    string `json:"kernel_name,omitempty"`
		Platform      string `json:"platform,omitempty"`
		KernelVersion string `json:"kernel_version,omitempty"`
		OsName        string `json:"os_name,omitempty"`
		KernelRelease string `json:"kernel_release,omitempty"`
		Drivers       struct {
			Ixgbe         string `json:"ixgbe,omitempty"`
			Ixgbevf       string `json:"ixgbevf,omitempty"`
			Mlx5Core      string `json:"mlx5_core,omitempty"`
			IbUverbs      string `json:"ib_uverbs,omitempty"`
			UioPciGeneric string `json:"uio_pci_generic,omitempty"`
		} `json:"drivers,omitempty"`
	} `json:"os_info,omitempty"`
	LastFailureCode        interface{} `json:"last_failure_code,omitempty"`
	CoresIds               []int       `json:"cores_ids,omitempty"`
	Memory                 int         `json:"memory,omitempty"`
	FrontendDedicatedCores int         `json:"frontend_dedicated_cores,omitempty"`
	FailureDomainType      string      `json:"failure_domain_type,omitempty"`
	LeadershipRole         interface{} `json:"leadership_role,omitempty"`
	StateChangedTime       time.Time   `json:"state_changed_time,omitempty"`
	Status                 string      `json:"status,omitempty"`
	Cores                  int         `json:"cores,omitempty"`
	HwMachineIdentifier    string      `json:"hw_machine_identifier,omitempty"`
	IsDedicated            bool        `json:"is_dedicated,omitempty"`
	LastFailure            interface{} `json:"last_failure,omitempty"`
	MgmtPort               int         `json:"mgmt_port,omitempty"`
	AutoRemoveTimeout      interface{} `json:"auto_remove_timeout,omitempty"`
	TotalScrubberLimit     int         `json:"total_scrubber_limit,omitempty"`
	ServerIdentifier       string      `json:"server_identifier,omitempty"`
	IoProcesses            int         `json:"io_processes,omitempty"`
	ContainerIp            string      `json:"container_ip,omitempty"`
}

type ContainersResponse []Container

func (c *ContainersResponse) SupportsPagination() bool {
	return true
}

func (c *ContainersResponse) CombinePartialResponse(next ApiObjectResponse) error {
	if nextContainers, ok := next.(*ContainersResponse); ok {
		*c = append(*c, *nextContainers...)
		return nil
	}
	return fmt.Errorf("cannot combine response of type %T with ContainersResponse", next)
}

func (a *ApiClient) getContainers(ctx context.Context) (*ContainersResponse, error) {
	a.containersLock.RLock()
	if a.containers != nil && a.containersUpdateTime.After(time.Now().Add(-time.Minute)) {
		a.containersLock.RUnlock()
		return a.containers, nil
	}
	a.containersLock.RUnlock()
	// recheck if during lock this was updated
	a.containersLock.RLock()
	if a.containers != nil && a.containersUpdateTime.After(time.Now().Add(-time.Minute)) {
		a.containersLock.RUnlock()
		return a.containers, nil
	}
	a.containersLock.RUnlock()
	a.containersLock.Lock()
	defer a.containersLock.Unlock()

	responseData := &ContainersResponse{}
	err := a.Get(ctx, ApiPathContainersInfo, nil, responseData)
	if err != nil {
		return responseData, err
	}
	a.containers = responseData
	a.containersUpdateTime = time.Now()
	return a.containers, nil
}

func (a *ApiClient) GetLocalContainer(ctx context.Context, allowProtocolContainers bool) (*Container, error) {
	logger := log.Ctx(ctx)
	containers, err := a.getLocalContainersFromDriver(ctx)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to fetch local containers from proc fs")
	}

	logger.Info().Msg("Fetching client containers from API")
	allContainers, err := a.getContainers(ctx)
	if err != nil {
		return nil, err
	}

	var ret []Container
	ret = filterFrontendContainers(ctx, *allContainers, containers, false)
	if len(ret) == 0 && allowProtocolContainers {
		logger.Trace().Msg("No frontend containers found, trying to find backend containers with frontend cores")
		ret = filterFrontendContainers(ctx, *allContainers, containers, true)
	}

	if len(ret) == 1 {
		return &ret[0], nil
	} else if len(ret) > 1 {
		logger.Trace().Msg("Found more than one local client container, selecting one randomly")
		return &ret[rand.IntnRange(0, len(ret))], nil
	} else {
		err := errors.New("could not find any local client container")
		logger.Error().Err(err).Msg("Cannot fetch local container")
		return nil, err
	}
}

type ProcFsContainer struct {
	ContainerName string
	Pid           int
	ContainerId   int
}

func (a *ApiClient) getLocalContainersFromDriver(ctx context.Context) ([]ProcFsContainer, error) {
	// this is the contents of /proc/wekafs/interface
	//IO node version 2e694b326b50d08a
	//mount:[wc-c88742862af5client:sbid-0x31a8] fs698 -o,writecache,inode_bits=auto,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0,container_name=c88742862af5client
	//mount:[wc-c88742862af5client:sbid-0x31a9] fs649 -o,writecache,inode_bits=auto,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0,container_name=c88742862af5client
	//mount:[wc-c88742862af5client:sbid-0x31aa] fs516 -o,writecache,inode_bits=auto,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0,container_name=c88742862af5client
	//mount:[wc-c88742862af5client:sbid-0x31ab] fs163 -o,writecache,inode_bits=auto,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0,container_name=c88742862af5client
	//GW driver_state: DRIVER_ACCEPTING
	//Active mounts: 4
	//Container=c88742862af5client FE 0: Connected frontend pid 4081176
	//Container=a88742862af5client FE 1: Connected frontend pid 4081177
	//Error counters
	//NS_num_enospc_errors: 0
	//NS_num_other_errors: 0
	//NS_num_sync_enospc: 0
	//NS_num_sync_retryable_errs: 0
	//NS_num_sync_other_errs: 0
	//NS_num_fenced_inodes: 0
	//NS_num_revoked_locks: 0
	//NS_dirty_bytes_dropped: 0
	// we need to parse all the lines with container_name=c88742862af5client and fetch container name AND pid

	const procFsPath = "/proc/wekafs/interface"
	logger := log.Ctx(ctx)
	f, err := os.Open(procFsPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var containers []ProcFsContainer
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) == 0 {
			continue
		}
		if !strings.Contains(line, "Container=") {
			continue
		}
		logger.Trace().Str("line", line).Msg("Found line in proc fs")
		//Container=a88742862af5client FE 1: Connected frontend pid 4081177
		name := strings.Split(line, "=")[1] // a88742862af5client FE 1: Connected frontend pid 4081177
		name = strings.Split(name, " ")[0]  // a88742862af5client

		if len(strings.Split(line, " ")) < 3 {
			logger.Error().Msg("Failed to parse container name and pid from proc fs line")
			continue
		}
		id := strings.Split(line, " ")[2] // 1: Connected frontend pid 4081177
		id = strings.Split(id, ":")[0]    // 1
		idInt, err := strconv.Atoi(id)
		if err != nil {
			logger.Error().Err(err).Msg("Failed to parse container id from proc fs")
			continue
		}

		pid := strings.Split(line, "pid ")[1]
		pidInt, err := strconv.Atoi(pid)
		if err != nil {
			logger.Error().Err(err).Msg("Failed to parse pid from proc fs")
			continue
		}
		cont := ProcFsContainer{
			ContainerName: name,
			Pid:           pidInt,
			ContainerId:   idInt,
		}
		containers = append(containers, cont)
	}
	return containers, nil
}

func GetLocalIpAddresses(ctx context.Context) ([]string, error) {
	interfaces, err := net.Interfaces()
	var ips []string
	if err != nil {
		return ips, err
	}
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ips = append(ips, ip.String())
		}
	}
	return ips, nil
}

func (a *ApiClient) EnsureLocalContainer(ctx context.Context, allowProtocolContainers bool) (string, error) {

	// already have the container name set either via secret or via API call
	if a.containerName != "" {
		return a.containerName, nil
	}
	// if having a local container name set in secrets
	if a.Credentials.LocalContainerName != "" {
		a.containerName = a.Credentials.LocalContainerName
		return a.containerName, nil
	}

	// if the cluster does not support multiple clusters, we must omit the container name since we can't pass it as a mount option
	if !a.SupportsMultipleClusters() {
		return "", nil
	}

	localContainers, err := a.getLocalContainersFromDriver(ctx)
	if err != nil {
		log.Ctx(ctx).Trace().Err(err).Msg("Failed to fetch local containers from proc fs")
	}

	// straigtforward case - if the cluster only has a single container in procfs
	if len(localContainers) == 1 {
		return localContainers[0].ContainerName, nil
	}

	// fetch the container name from the API
	container, err := a.GetLocalContainer(ctx, allowProtocolContainers)
	if err != nil {
		return "", err
	}
	a.containerName = container.ContainerName
	return a.containerName, nil
}

func filterFrontendContainers(ctx context.Context, containerList []Container, procFsContainers []ProcFsContainer, allowProtocolContainers bool) []Container {
	logger := log.Ctx(ctx)
	var ret []Container
	procFsContainerNames := make([]string, 0, len(procFsContainers))
	for _, procFsContainer := range procFsContainers {
		procFsContainerNames = append(procFsContainerNames, procFsContainer.ContainerName)
	}

	localIpAddresses, err := GetLocalIpAddresses(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to fetch local IP addresses")
	}

	for _, container := range containerList {
		// filter out containers that are not matching our procfs container names but only if we have procfs containers resolved
		if len(procFsContainers) > 0 {
			if !slices.Contains(procFsContainerNames, container.ContainerName) {
				continue
			}
		}

		if len(localIpAddresses) > 0 {
			// if the container IP is not in the local IP addresses, skip it
			if !slices.Contains(localIpAddresses, container.ContainerIp) {
				logger.Trace().Str("container_ip", container.ContainerIp).Msg("Skipping a container with IP not matching local IPs")
				continue
			}
		}

		// at this stage we have a container that matches our procfs containers and has a local IP address, while hostname is not a good match.
		if container.Mode == "backend" {
			if container.FrontendDedicatedCores >= 1 && allowProtocolContainers {
				logger.Trace().Str("container_hostname", container.Hostname).Msg("Found a backend container with frontend cores, will use it as a frontend container")
				ret = append(ret, container)
				continue
			}
			logger.Trace().Str("container_hostname", container.Hostname).Msg("Skipping a backend container")
			continue
		}
		if container.State != "ACTIVE" || container.Status != "UP" {
			logger.Trace().Str("container_hostname", container.Hostname).
				Str("container_state", container.State).
				Str("container_status", container.Status).
				Str("container_id", container.Id).
				Msg("Skipping an INACTIVE container")
			continue
		}
		logger.Debug().Str("container_hostname", container.Hostname).Str("container_name", container.ContainerName).Msg("Found a valid container")
		ret = append(ret, container)
	}
	return ret
}

func (a *ApiClient) fetchUserRoleAndOrgId(ctx context.Context) {
	logger := log.Ctx(ctx)
	ret := &WhoamiResponse{}
	err := a.Request(ctx, "GET", ApiPathWhoami, nil, nil, ret)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to fetch user role. Assuming old cluster version")
		return
	}
	if ret != nil {
		a.ApiUserRole = ret.Role
		a.ApiOrgId = ret.OrgId
	}
}

func (a *ApiClient) ensureSufficientPermissions(ctx context.Context) error {
	logger := log.Ctx(ctx)
	a.fetchUserRoleAndOrgId(ctx)
	if a.ApiUserRole == "" {
		logger.Error().Msg("Could not determine user role, assuming old version of WEKA cluster")
	}
	if !a.HasCSIPermissions() {
		logger.Error().Str("username", a.Credentials.Username).Str("role", string(a.ApiUserRole)).Msg("User does not have necessary CSI permissions and cannot be used. Refer to WEKA CSI Plugin /documentation")
		return errors.New(fmt.Sprintf("user %s does not have sufficient permissions for performing CSI operations", a.Credentials.Username))
	}
	return nil
}
