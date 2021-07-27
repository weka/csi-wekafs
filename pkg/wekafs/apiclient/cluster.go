package apiclient

import (
	"fmt"
	"github.com/google/uuid"
)

const ApiPathLogin = "login"

const ApiPathTokenExpiry = "security/tokensExpiry"

const ApiPathRefresh = "login/refresh"

const ApiPathClusterInfo = "cluster"

//updateRefreshTokenInterval fetches the refresh token expiry from API
func (a *ApiClient) updateRefreshTokenInterval() error {
	responseData := &TokenExpiryResponse{}
	if err := a.Get(ApiPathTokenExpiry, nil, responseData); err != nil {
		return err
	}
	a.refreshTokenExpiryInterval = responseData.RefreshTokenExpiry
	a.Log(3, "Updated refresh token validity period")
	return nil
}

// fetchClusterInfo performed each login and checks for version
func (a *ApiClient) fetchClusterInfo() error {
	a.Log(4, "Checking for Weka cluster version...")
	responseData := &ClusterInfoResponse{}
	if err := a.Get(ApiPathClusterInfo, nil, responseData); err != nil {
		return err
	}
	a.ClusterName = responseData.Name
	a.ClusterGuid = responseData.Guid
	clusterVersion := fmt.Sprintf("v%s", responseData.Release)
	a.CompatibilityMap.fillIn(clusterVersion)
	a.Log(3, "Cluster compatibility for filesystem as CSI volume:", a.SupportsFilesystemAsVolume())
	a.Log(3, "Cluster compatibility for quota directory as CSI volume:", a.SupportsQuotaDirectoryAsVolume())
	a.Log(3, "Cluster compatibility for regular directory as CSI volume:", a.SupportsDirectoryAsVolume())
	return nil
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

type ClusterInfoResponse struct {
	Name        string    `json:"name"`
	ReleaseHash string    `json:"release_hash"`
	InitStage   string    `json:"init_stage"`
	Release     string    `json:"release"`
	Guid        uuid.UUID `json:"guid"`
}
