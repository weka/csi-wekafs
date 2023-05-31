package apiclient

import (
	"context"
	"fmt"
	"github.com/google/uuid"
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
