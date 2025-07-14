package apiclient

import (
	"context"
	"github.com/rs/zerolog/log"
	"golang.org/x/exp/maps"
	"time"
)

// Login logs into API, updates refresh token expiry
func (a *ApiClient) Login(ctx context.Context) error {
	logger := log.Ctx(ctx)
	if a.isLoggedIn() {
		return nil
	}
	a.Lock()
	defer a.Unlock()
	r := LoginRequest{
		Username: a.Credentials.Username,
		Password: a.Credentials.Password,
		Org:      a.Credentials.Organization,
	}
	jb, err := marshalRequest(r)
	if err != nil {
		return err
	}
	responseData := &LoginResponse{}
	if err := a.request(ctx, "POST", ApiPathLogin, jb, nil, responseData); err != nil {
		if err.getType() == "ApiAuthorizationError" {
			logger.Error().Err(err).Str("endpoint", a.getEndpoint(ctx).String()).Msg("Could not log in to endpoint")
		}
		logger.Error().Err(err).Msg("")
		return err
	}
	a.apiToken = responseData.AccessToken
	a.refreshToken = responseData.RefreshToken
	a.apiTokenExpiryDate = time.Now().Add(time.Duration(responseData.ExpiresIn-30) * time.Second)
	if a.refreshTokenExpiryInterval < 1 {
		_ = a.updateTokensExpiryInterval(ctx)
	}
	a.refreshTokenExpiryDate = time.Now().Add(time.Duration(a.refreshTokenExpiryInterval) * time.Second)

	err = a.ensureSufficientPermissions(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to ensure sufficient permissions for supplied credentials. Cannot continue")
		return err
	}

	if err := a.fetchClusterInfo(ctx); err != nil {
		logger.Error().Err(err).Msg("Failed to fetch information from Weka cluster on login")
		return err
	}
	logger.Debug().Msg("Successfully connected to cluster API")

	if a.Credentials.AutoUpdateEndpoints {
		if err := a.UpdateApiEndpoints(ctx); err != nil {
			logger.Error().Err(err).Msg("Failed to update actual API endpoints")
		} else {
			logger.Debug().Strs("new_api_endpoints", maps.Keys(a.actualApiEndpoints)).Str("current_endpoint", a.getEndpoint(ctx).String()).Msg("Updated API endpoints")
		}
	} else {
		logger.Debug().Str("current_endpoint", a.getEndpoint(ctx).String()).Msg("Auto update of API endpoints is disabled")
	}
	return nil
}

// Init checks if API token refresh is required and transparently refreshes or fails back to (re)login
func (a *ApiClient) Init(ctx context.Context) error {
	if a.metrics == nil {
		a.metrics = NewApiMetrics(a)
	}
	if a.apiTokenExpiryDate.After(time.Now()) {
		return nil
	} else {
		log.Ctx(ctx).Trace().TimeDiff("valid_for", a.apiTokenExpiryDate, time.Now()).Msg("Auth token is expired")
	}
	if !a.isLoggedIn() {
		log.Ctx(ctx).Trace().Msg("Client is not authenticated, logging in...")
		return a.Login(ctx)
	}

	r := RefreshRequest{RefreshToken: a.refreshToken}
	responseData := &RefreshResponse{}
	payload, _ := marshalRequest(r)
	if err := a.request(ctx, "POST", ApiPathRefresh, payload, nil, responseData); err != nil {
		log.Ctx(ctx).Trace().Msg("Failed to refresh auth token, logging in...")
		return a.Login(ctx)
	}
	a.refreshToken = responseData.RefreshToken
	a.apiToken = responseData.AccessToken
	a.apiTokenExpiryDate = time.Now().Add(time.Duration(a.apiTokenExpiryInterval-30) * time.Second)
	log.Ctx(ctx).Trace().TimeDiff("valid_for", a.refreshTokenExpiryDate, time.Now()).Msg("Auth token is valid")
	return nil
}

// isLoggedIn returns true if client has a refresh token and it is not expired so it can refresh or perform ops directly
func (a *ApiClient) isLoggedIn() bool {
	if a.apiToken == "" {
		return false
	}
	if a.refreshTokenExpiryDate.Before(time.Now()) && a.refreshTokenExpiryInterval > 0 {
		return false
	}
	return true
}

func (a *ApiClient) HasCSIPermissions() bool {
	if a.ApiUserRole != "" {
		return a.ApiUserRole == ApiUserRoleCSI || a.ApiUserRole == ApiUserRoleClusterAdmin || a.ApiUserRole == ApiUserRoleOrgAdmin
	}
	return false
}
