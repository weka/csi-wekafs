package apiclient

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"time"
)

const ApiPathLogin = "login"

const ApiPathTokenExpiry = "security/defaultTokensExpiry"

const ApiPathRefresh = "login/refresh"

const ApiPathClusterInfo = "cluster"

const ApiContainersInfo = "containers"

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
	logger.Info().Msg(fmt.Sprintf("Cluster compatibility for quota on non-empty CSI volume: %t", a.SupportsQuotaOnNonEmptyDirs()))
	logger.Info().Msg(fmt.Sprintf("Cluster compatibility for regular directory as CSI volume: %t", a.SupportsDirectoryAsVolume()))
	logger.Info().Msg(fmt.Sprintf("Cluster compatibility for authenticated filesystem mounts: %t", a.SupportsAuthenticatedMounts()))
	logger.Info().Msg(fmt.Sprintf("Cluster compatibility for new filesystem from snapshot: %t", a.SupportsNewFileSystemFromSnapshot()))
	logger.Info().Msg(fmt.Sprintf("Cluster compatibility for cloning filesystems: %t", a.SupportsFilesystemCloning()))
	logger.Info().Msg(fmt.Sprintf("Cluster compatibility for supporting multiple connections: %t", a.SupportsMultipleClusters()))
	logger.Info().Msg(fmt.Sprintf("Cluster requires using new API path for nodes (nodes->processes): %t", a.RequiresNewNodePath()))
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

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token,omitempty"`
}

type RefreshResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

type TokenExpiryResponse struct {
	AccessTokenExpiry  int64 `json:"access_token_expiry"`
	RefreshTokenExpiry int64 `json:"refresh_token_expiry"`
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

func (a *ApiClient) getContainers(ctx context.Context) (*ContainersResponse, error) {
	responseData := &ContainersResponse{}
	err := a.Get(ctx, ApiContainersInfo, nil, responseData)
	return responseData, err
}

func (a *ApiClient) GetLocalContainer(ctx context.Context) (*Container, error) {
	logger := log.Ctx(ctx)
	logger.Info().Str("hostname", a.hostname).Msg("Fetching client containers on host")
	allContainers, err := a.getContainers(ctx)
	if err != nil {
		return nil, err
	}

	var ret []Container
	for _, container := range *allContainers {
		if container.Hostname == a.hostname {
			if container.Mode == "backend" {
				logger.Trace().Str("container_hostname", container.Hostname).Msg("Skipping a backend container")
				continue
			}
			if container.State != "ACTIVE" {
				logger.Trace().Str("container_hostname", container.Hostname).Msg("Skipping an INACTIVE container")
				continue
			}
			logger.Debug().Str("container_hostname", container.Hostname).Msg("Found a valid container")
			ret = append(ret, container)
		}
	}
	if len(ret) == 1 {
		return &ret[0], nil
	} else if len(ret) > 1 {
		err := errors.New("could not determine local client containers, ambiguous hostname")
		logger.Error().Err(err).Msg("Cannot fetch local container")
		return nil, err
	} else {
		err := errors.New("could not find any local client container")
		logger.Error().Err(err).Msg("Cannot fetch local container")
		return nil, err
	}
}
