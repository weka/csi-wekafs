package apiclient

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/hashicorp/go-version"
	"github.com/rs/zerolog/log"
)

const ApiPathLogin = "login"

const ApiPathTokenExpiry = "security/defaultTokensExpiry"

const ApiPathRefresh = "login/refresh"

const ApiPathClusterInfo = "cluster"

//updateTokensExpiryInterval fetches the refresh token expiry from API
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
	logger := log.Ctx(ctx).With().Str("credentials", a.Credentials.String()).Logger()
	logger.Debug().Msg("Checking for Weka cluster version...")
	responseData := &ClusterInfoResponse{}
	if err := a.Get(ctx, ApiPathClusterInfo, nil, responseData); err != nil {
		return err
	}
	a.ClusterName = responseData.Name
	a.ClusterGuid = responseData.Guid
	clusterVersion := fmt.Sprintf("v%s", responseData.Release)
	v, _ := version.NewVersion(clusterVersion)
	a.CompatibilityMap.fillIn(clusterVersion)
	logger.Info().Str("cluster_guid", a.ClusterGuid.String()).Str("cluster_name", a.ClusterName).
		Str("cluster_version", clusterVersion).Str("parsed_version", v.String()).Msg("Successfully connected to cluster")
	logger.Info().Msg(fmt.Sprintln("Cluster compatibility for filesystem as CSI volume:", a.SupportsFilesystemAsVolume()))
	logger.Info().Msg(fmt.Sprintln("Cluster compatibility for quota directory as CSI volume:", a.SupportsQuotaDirectoryAsVolume()))
	logger.Info().Msg(fmt.Sprintln("Cluster compatibility for quota on non-empty CSI volume:", a.SupportsQuotaOnNonEmptyDirs()))
	logger.Info().Msg(fmt.Sprintln("Cluster compatibility for regular directory as CSI volume:", a.SupportsDirectoryAsVolume()))
	logger.Info().Msg(fmt.Sprintln("Cluster compatibility for authenticated filesystem mounts", a.SupportsAuthenticatedMounts()))
	logger.Info().Msg(fmt.Sprintln("Cluster compatibility for new filesystem from snapshot", a.SupportsNewFileSystemFromSnapshot()))
	logger.Info().Msg(fmt.Sprintln("Cluster compatibility for cloning filesystems", a.SupportsFilesystemCloning()))
	return nil
}

func (a *ApiClient) GetFreeCapacity(ctx context.Context) (uint64, error) {
	responseData := &ClusterInfoResponse{}
	if err := a.Get(ctx, ApiPathClusterInfo, nil, responseData); err != nil {
		return 0, err
	}
	capacity := responseData.Capacity.UnprovisionedBytes
	log.Ctx(ctx).Debug().Uint64("free_capacity", capacity).Msg("Obtained cluster free capacity")
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
