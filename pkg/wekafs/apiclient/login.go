package apiclient

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

type loginInProgressKey struct{}

// withLoginInProgress marks ctx as running inside Login, and loginInProgress reports it. Login makes
// several requests after authenticating, each of which passes through Init; without this marker a
// token that came back already near expiry would send Init back into Login on the same goroutine,
// which is holding loginMu — a self-deadlock. The marker travels with the context, so it applies to
// exactly the call chain Login started and to no other goroutine.
func withLoginInProgress(ctx context.Context) context.Context {
	return context.WithValue(ctx, loginInProgressKey{}, true)
}

func loginInProgress(ctx context.Context) bool {
	inProgress, _ := ctx.Value(loginInProgressKey{}).(bool)
	return inProgress
}

// Login logs into API, updates refresh token expiry
func (a *ApiClient) Login(ctx context.Context) error {
	logger := log.Ctx(ctx)
	if a.loginSucceeded() {
		return nil
	}
	// loginMu serialises login attempts; it is deliberately NOT the lock guarding the client state.
	// Logging in makes four round-trips, and holding the state lock across them would both block
	// every concurrent reader for the duration and deadlock, since the requests those calls issue
	// take the state lock themselves to read the auth token.
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	// Re-check: another goroutine may have completed a login while we waited for loginMu. This must
	// test loginSucceeded, not isLoggedIn: isLoggedIn goes true the moment a token is published,
	// which happens below *before* the post-auth setup (permissions, cluster info, endpoints) that
	// can still fail. Testing isLoggedIn here would report success for a login that is about to
	// return an error, leaving every waiter with an all-false CompatibilityMap.
	if a.loginSucceeded() {
		return nil
	}
	ctx = withLoginInProgress(ctx)
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
	if _, err := a.request(ctx, "POST", ApiPathLogin, jb, nil, responseData); err != nil {
		if err.getType() == "ApiAuthorizationError" {
			logger.Error().Err(err).Str("endpoint", a.getEndpoint(ctx).String()).Msg("Could not log in to endpoint")
		}
		logger.Error().Err(err).Msg("")
		return err
	}
	// Publish the token before the calls below, which issue requests of their own and need it to
	// authenticate. loginComplete is cleared in the same critical section: from this point isLoggedIn
	// is true but the login is not yet done, and a concurrent caller must not be told otherwise.
	a.Lock()
	a.apiToken = responseData.AccessToken
	a.refreshToken = responseData.RefreshToken
	a.apiTokenExpiryDate = time.Now().Add(time.Duration(responseData.ExpiresIn-30) * time.Second)
	a.loginComplete = false
	needExpiryInterval := a.refreshTokenExpiryInterval < 1
	a.Unlock()

	if needExpiryInterval {
		_ = a.updateTokensExpiryInterval(ctx)
	}
	a.Lock()
	a.refreshTokenExpiryDate = time.Now().Add(time.Duration(a.refreshTokenExpiryInterval) * time.Second)
	a.Unlock()

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
			logger.Debug().Strs("new_api_endpoints", a.apiEndpoints.Keys()).Str("current_endpoint", a.getEndpoint(ctx).String()).Msg("Updated API endpoints")
		}
	} else {
		logger.Debug().Str("current_endpoint", a.getEndpoint(ctx).String()).Msg("Auto update of API endpoints is disabled")
	}
	// Everything that determines whether the client can actually operate (permissions, cluster info)
	// has succeeded at this point; a failed UpdateApiEndpoints above was already logged and does not
	// fail Login, so it must not hold back loginComplete either.
	a.Lock()
	a.loginComplete = true
	a.Unlock()
	return nil
}

// Init checks if API token refresh is required and transparently refreshes or fails back to (re)login
func (a *ApiClient) Init(ctx context.Context) error {
	if loginInProgress(ctx) {
		// A login is already under way on this call chain and has published its token; the requests it
		// makes to finish setting up must not try to authenticate again.
		return nil
	}
	a.RLock()
	tokenExpiry := a.apiTokenExpiryDate
	a.RUnlock()
	if tokenExpiry.After(time.Now()) {
		return nil
	}
	log.Ctx(ctx).Trace().TimeDiff("valid_for", tokenExpiry, time.Now()).Msg("Auth token is expired")
	if !a.isLoggedIn() {
		log.Ctx(ctx).Trace().Msg("Client is not authenticated, logging in...")
		return a.Login(ctx)
	}

	a.RLock()
	r := RefreshRequest{RefreshToken: a.refreshToken}
	a.RUnlock()
	responseData := &RefreshResponse{}
	payload, _ := marshalRequest(r)
	if _, err := a.request(ctx, "POST", ApiPathRefresh, payload, nil, responseData); err != nil {
		log.Ctx(ctx).Trace().Msg("Failed to refresh auth token, logging in...")
		return a.Login(ctx)
	}
	a.Lock()
	a.refreshToken = responseData.RefreshToken
	a.apiToken = responseData.AccessToken
	a.apiTokenExpiryDate = time.Now().Add(time.Duration(a.apiTokenExpiryInterval-30) * time.Second)
	refreshExpiry := a.refreshTokenExpiryDate
	a.Unlock()
	log.Ctx(ctx).Trace().TimeDiff("valid_for", refreshExpiry, time.Now()).Msg("Auth token is valid")
	return nil
}

// authToken returns the bearer token to authenticate requests with, or an empty string if the client
// is not logged in. Returning the token under the same lock that tests for it keeps a concurrent
// login from swapping it in between the two.
func (a *ApiClient) authToken() string {
	a.RLock()
	defer a.RUnlock()
	if !a.isLoggedInLocked() {
		return ""
	}
	return a.apiToken
}

// isLoggedIn returns true if client has a refresh token and it is not expired so it can refresh or perform ops directly
func (a *ApiClient) isLoggedIn() bool {
	a.RLock()
	defer a.RUnlock()
	return a.isLoggedInLocked()
}

// isLoggedInLocked is isLoggedIn for callers that already hold the lock.
// REQUIRES: a's read or write lock is held by the caller.
func (a *ApiClient) isLoggedInLocked() bool {
	if a.apiToken == "" {
		return false
	}
	if a.refreshTokenExpiryDate.Before(time.Now()) && a.refreshTokenExpiryInterval > 0 {
		return false
	}
	return true
}

// loginSucceeded reports whether the client is not just holding a token, but finished a login
// completely: isLoggedIn alone goes true the instant a token is published, before the post-auth
// setup that follows can still fail. Login's own re-check under loginMu, and its opportunistic
// check before taking loginMu at all, both need this rather than isLoggedIn - otherwise a goroutine
// racing a login whose setup is still in flight (or has already failed) is told it succeeded.
func (a *ApiClient) loginSucceeded() bool {
	a.RLock()
	defer a.RUnlock()
	return a.isLoggedInLocked() && a.loginComplete
}

// userRole returns the role established at login, which fetchUserRoleAndOrgId rewrites on every
// re-login while requests are in flight.
func (a *ApiClient) userRole() ApiUserRole {
	a.RLock()
	defer a.RUnlock()
	return a.ApiUserRole
}

func (a *ApiClient) HasCSIPermissions() bool {
	role := a.userRole()
	if role != "" {
		return role == ApiUserRoleCSI || role == ApiUserRoleClusterAdmin || role == ApiUserRoleOrgAdmin
	}
	return false
}
