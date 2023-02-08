package apiclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"hash/fnv"
	"io/ioutil"
	"k8s.io/helm/pkg/urlutil"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	ApiHttpTimeOutSeconds         = 60
	ApiRetryIntervalSeconds       = 1
	ApiRetryMaxCount              = 5
	RetryBackoffExponentialFactor = 1
	RootOrganizationName          = "Root"
	TracerName                    = "weka-csi"
)

//ApiClient is a structure that defines Weka API client
// client: http.Client ref
// Username, Password - obvious
// HttpScheme: either 'http', 'https'
// Endpoints: slice of 'ip_address_or_fqdn:port' strings
// apiToken, refreshToken, apiTokenExpiryDate used for bearer auth
// currentEndpointId: refers to the currently working API endpoint
// Timeout sets max request timeout duration
type ApiClient struct {
	sync.Mutex
	client                     *http.Client
	Credentials                Credentials
	ClusterGuid                uuid.UUID
	ClusterName                string
	MountEndpoints             []string
	currentEndpointId          int
	apiToken                   string
	apiTokenExpiryDate         time.Time
	refreshToken               string
	apiTokenExpiryInterval     int64
	refreshTokenExpiryInterval int64
	refreshTokenExpiryDate     time.Time
	Timeout                    time.Duration
	CompatibilityMap           *WekaCompatibilityMap
	clientHash                 uint32
}

func NewApiClient(ctx context.Context, credentials Credentials, allowInsecureHttps bool) (*ApiClient, error) {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: allowInsecureHttps},
	}
	a := &ApiClient{
		Mutex: sync.Mutex{},
		client: &http.Client{
			Transport:     tr,
			CheckRedirect: nil,
			Jar:           nil,
			Timeout:       0,
		},
		ClusterGuid:       uuid.UUID{},
		Credentials:       credentials,
		CompatibilityMap:  &WekaCompatibilityMap{},
		Timeout:           time.Duration(ApiHttpTimeOutSeconds) * time.Second,
		currentEndpointId: -1,
	}
	log.Ctx(ctx).Trace().Bool("insecure_skip_verify", allowInsecureHttps).Msg("Creating new API client")
	a.clientHash = a.generateHash()
	return a, nil
}

// fetchMountEndpoints used to obtain actual data plane IP addresses
func (a *ApiClient) fetchMountEndpoints(ctx context.Context) error {
	log.Ctx(ctx).Trace().Msg("Fetching mount endpoints")
	a.MountEndpoints = []string{}
	nodes := &[]WekaNode{}
	err := a.GetNodesByRole(ctx, NodeRoleBackend, nodes)
	if err != nil {
		return err
	}
	for _, n := range *nodes {
		for _, i := range n.Ips {
			a.MountEndpoints = append(a.MountEndpoints, i)
		}
	}
	return nil
}

// UpdateEndpoints fetches current management IP addresses of the cluster
func (a *ApiClient) UpdateEndpoints(endpoints []string) {
	a.Lock()
	defer a.Unlock()
	a.Credentials.Endpoints = endpoints
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

//rotateEndpoint returns a random endpoint of the configured ones
func (a *ApiClient) rotateEndpoint(ctx context.Context) {
	logger := log.Ctx(ctx)
	if a.Credentials.Endpoints == nil || len(a.Credentials.Endpoints) == 0 {
		a.currentEndpointId = -1
		logger.Error().Msg("Failed to choose random endpoint, no endpoints exist")
		return
	}
	//a.currentEndpointId = rand.Intn(len(a.Credentials.Endpoints))
	a.currentEndpointId = (a.currentEndpointId + 1) % len(a.Credentials.Endpoints)

	logger.Trace().Str("current_endpoint", a.getEndpoint(ctx)).Msg("Switched to new API endpoint")
}

//getEndpoint returns last known endpoint to work against
func (a *ApiClient) getEndpoint(ctx context.Context) string {
	if a.currentEndpointId < 0 {
		a.rotateEndpoint(ctx)
	}
	return a.Credentials.Endpoints[a.currentEndpointId]
}

//getBaseUrl returns the full HTTP URL of the API endpoint including schema, chosen endpoint and API prefix
func (a *ApiClient) getBaseUrl(ctx context.Context) string {
	scheme := ""
	switch strings.ToUpper(a.Credentials.HttpScheme) {

	case "HTTP":
		scheme = "http"
	case "HTTPS":
		scheme = "https"
	default:
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s/api/v2", scheme, a.getEndpoint(ctx))
}

// do Makes a basic API call to the client, returns an *ApiResponse that includes raw data, error message etc.
func (a *ApiClient) do(ctx context.Context, Method string, Path string, Payload *[]byte, Query url.Values) (*ApiResponse, apiError) {
	//construct URL path
	if len(a.Credentials.Endpoints) < 1 {
		return &ApiResponse{}, &ApiNoEndpointsError{
			Err: errors.New("no endpoints could be found for API client"),
		}
	}
	u := a.getUrl(ctx, Path)

	//construct base request and add auth if exists
	var body *bytes.Reader
	if Payload != nil {
		body = bytes.NewReader(*Payload)
	} else {
		body = bytes.NewReader([]byte(""))
	}
	r, err := http.NewRequest(Method, u, body)
	if err != nil {
		return nil, &ApiError{
			Err:         err,
			Text:        "Failed to construct API request",
			StatusCode:  0,
			RawData:     nil,
			ApiResponse: nil,
		}
	}
	r.Header.Set("content-type", "application/json")
	if a.isLoggedIn() {
		r.Header.Set("Authorization", fmt.Sprintf("Bearer %s", a.apiToken))
	}

	//add query params
	if Query != nil && len(Query) > 0 && a.SupportsUrlQueryParams() {
		r.URL.RawQuery = Query.Encode()
	}

	payload := ""
	if Payload != nil {
		payload = string(*Payload)
	}
	logger := log.Ctx(ctx)
	logger.Trace().Str("method", Method).Str("url", r.URL.RequestURI()).Str("payload", payload).Msg("")

	response, err := a.client.Do(r)
	if err != nil {
		return nil, &transportError{err}
	}

	if response == nil {
		return nil, &transportError{errors.New("received no response")}
	}

	responseBody, err := ioutil.ReadAll(response.Body)
	logger.Trace().Str("response", string(responseBody)).Msg("")
	if err != nil {
		return nil, &ApiInternalError{
			Err:         err,
			Text:        fmt.Sprintf("Failed to parse response: %s", err.Error()),
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: nil,
		}
	}

	defer func() {
		_ = response.Body.Close()
	}()

	Response := &ApiResponse{}
	err = json.Unmarshal(responseBody, Response)
	Response.HttpStatusCode = response.StatusCode
	if err != nil {
		logger.Error().Err(err).Int("http_status_code", Response.HttpStatusCode).Msg("Could not parse response JSON")
		return nil, &ApiError{
			Err:         err,
			Text:        "Failed to parse HTTP response body",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	}

	switch response.StatusCode {
	case http.StatusOK: //200
		return Response, nil
	case http.StatusCreated: //201
		return Response, nil
	case http.StatusAccepted: //202
		return Response, nil
	case http.StatusNoContent: //203
		return Response, nil
	case http.StatusBadRequest: //400
		return Response, &ApiBadRequestError{
			Err:         nil,
			Text:        "Operation failed",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusUnauthorized: //401
		return Response, &ApiAuthorizationError{
			Err:         nil,
			Text:        "Operation failed",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusNotFound: //404
		return Response, &ApiNotFoundError{
			Err:         nil,
			Text:        "Object not found",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusConflict: //409
		return Response, &ApiConflictError{
			ApiError: ApiError{
				Err:         nil,
				Text:        "Object conflict",
				StatusCode:  response.StatusCode,
				RawData:     &responseBody,
				ApiResponse: Response,
			},
			ConflictingEntityId: nil, //TODO: parse and provide entity ID when supplied by API
		}

	case http.StatusInternalServerError: //500
		return Response, ApiInternalError{
			Err:         nil,
			Text:        Response.Message,
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}

	default:
		return Response, ApiError{
			Err:         err,
			Text:        "General failure during API command",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	}
}

// handleNetworkErrors checks if the error returned by endpoint is a network error (transient by definition)
func (a *ApiClient) handleNetworkErrors(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if netError, ok := err.(net.Error); ok && netError.Timeout() {
		return &ApiNetworkError{Err: errors.New(fmt.Sprintln("Connection timed out to ", a.getEndpoint(ctx)))}
	} else {
		switch t := err.(type) {
		case *net.OpError:
			if t.Op == "dial" {
				return &ApiNetworkError{Err: errors.New(fmt.Sprintln("Unknown host", a.getEndpoint(ctx)))}
			} else if t.Op == "read" {
				return &ApiNetworkError{Err: errors.New(fmt.Sprintln("Connection refused:", a.getEndpoint(ctx)))}
			}

		case syscall.Errno:
			if t == syscall.ECONNREFUSED {
				return &ApiNetworkError{Err: errors.New(fmt.Sprintln("Connection refused:", a.getEndpoint(ctx)))}
			}
		}
	}
	// In this case this is not a network error, will be treated separately
	return nil
}

// request wraps do with retries and some more error handling
func (a *ApiClient) request(ctx context.Context, Method string, Path string, Payload *[]byte, Query url.Values, v interface{}) apiError {
	err := a.retryBackoff(ctx, ApiRetryMaxCount, time.Second*time.Duration(ApiRetryIntervalSeconds), func() apiError {
		rawResponse, reqErr := a.do(ctx, Method, Path, Payload, Query)
		logger := log.Ctx(ctx)
		if a.handleNetworkErrors(ctx, reqErr) != nil { // transient network errors
			a.rotateEndpoint(ctx)
			logger.Error().Err(reqErr).Msg("")
			return reqErr
		}
		if reqErr != nil {
			return ApiNonrecoverableError{reqErr}
		}
		s := rawResponse.HttpStatusCode
		var responseCodes []string
		if len(rawResponse.ErrorCodes) > 0 {
			logger.Error().Strs("error_codes", rawResponse.ErrorCodes).Msg("Failed to execute request")
			for _, code := range rawResponse.ErrorCodes {
				if code != "OperationFailedException" {
					responseCodes = append(responseCodes, code)
				}
			}
			return ApiNonrecoverableError{
				apiError: reqErr,
			}
		}
		err := json.Unmarshal(rawResponse.Data, v)
		if err != nil {
			logger.Error().Err(err).Interface("object_type", reflect.TypeOf(v)).Msg("Failed to marshal JSON request into a valid interface")
		}
		switch s {
		case http.StatusOK:
			return nil
		case http.StatusUnauthorized:
			logger.Warn().Msg("Got Authorization failure on request, trying to re-login")
			_ = a.Init(ctx)
			return reqErr
		case http.StatusNotFound, http.StatusConflict, http.StatusBadRequest, http.StatusInternalServerError:
			return ApiNonrecoverableError{reqErr}
		default:
			logger.Warn().Err(reqErr).Int("http_code", s).Msg("Failed to perform a request, got an unhandled error")
			return ApiNonrecoverableError{reqErr}
		}
	})
	if err != nil {
		return err.(apiError)
	}
	return nil
}

// Request makes sure that client is logged in and has a non-expired token
func (a *ApiClient) Request(ctx context.Context, Method string, Path string, Payload *[]byte, Query url.Values, Response interface{}) error {
	if err := a.Init(ctx); err != nil {
		log.Ctx(ctx).Error().Err(err).Msg("Failed to re-authenticate on repeating request")
		return err
	}
	err := a.request(ctx, Method, Path, Payload, Query, Response)
	if err != nil {
		return err
	}
	return nil
}

// Get is shortcut for Request("GET" ...)
func (a *ApiClient) Get(ctx context.Context, Path string, Query url.Values, Response interface{}) error {
	return a.Request(ctx, "GET", Path, nil, Query, Response)
}

// Post is shortcut for Request("POST" ...)
func (a *ApiClient) Post(ctx context.Context, Path string, Payload *[]byte, Query url.Values, Response interface{}) error {
	return a.Request(ctx, "POST", Path, Payload, Query, Response)
}

// Put is shortcut for Request("PUT" ...)
func (a *ApiClient) Put(ctx context.Context, Path string, Payload *[]byte, Query url.Values, Response interface{}) error {
	return a.Request(ctx, "PUT", Path, Payload, Query, Response)
}

// Delete is shortcut for Request("DELETE" ...)
func (a *ApiClient) Delete(ctx context.Context, Path string, Payload *[]byte, Query url.Values, Response interface{}) error {
	return a.Request(ctx, "DELETE", Path, Payload, Query, Response)
}

// getUrl returns a URL which consists of baseUrl + path
func (a *ApiClient) getUrl(ctx context.Context, path string) string {
	u, _ := urlutil.URLJoin(a.getBaseUrl(ctx), path)
	return u
}

// Login logs into API, updates refresh token expiry
func (a *ApiClient) Login(ctx context.Context) error {
	oldCtx := ctx
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
	logger.Debug().Msg("Logging in. For safety, API logging is suppressed")
	ctx = log.Ctx(ctx).Level(zerolog.Disabled).With().Str("credentials", a.Credentials.String()).Logger().WithContext(ctx)
	if err := a.request(ctx, "POST", ApiPathLogin, jb, nil, responseData); err != nil {
		if err.getType() == "ApiAuthorizationError" {
			logger.Error().Err(err).Str("endpoint", a.getEndpoint(ctx)).Msg("Could not log in to endpoint")
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
	ctx = oldCtx
	a.refreshTokenExpiryDate = time.Now().Add(time.Duration(a.refreshTokenExpiryInterval) * time.Second)
	_ = a.fetchClusterInfo(ctx)
	logger.Debug().Msg("Successfully connected to cluster API")
	return nil
}

// generateHash used for storing multiple clients in hash table. Hash() is created once as connection params might change
func (a *ApiClient) generateHash() uint32 {
	h := fnv.New32a()
	s := fmt.Sprintln(a.Credentials.Username, a.Credentials.Password, a.Credentials.Organization, a.Credentials.Endpoints)
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// Hash returns the client hash as it was generated once client was initialized
func (a *ApiClient) Hash() uint32 {
	return a.clientHash
}

// Init checks if API token refresh is required and transparently refreshes or fails back to (re)login
func (a *ApiClient) Init(ctx context.Context) error {
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

// marshalRequest converts interface to bytes
func marshalRequest(r interface{}) (*[]byte, error) {
	j, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// retryBackoff performs operation and retries on transient failures. Does not retry on ApiNonrecoverableError
func (a *ApiClient) retryBackoff(ctx context.Context, attempts int, sleep time.Duration, f func() apiError) error {
	maxAttempts := attempts
	if err := f(); err != nil {
		switch s := err.(type) {
		case ApiNonrecoverableError:
			log.Ctx(ctx).Debug().Msg("Non-recoverable error occurred, stopping further attempts")
			// Return the original error for later checking
			return s.apiError
		}
		if attempts--; attempts > 0 {
			log.Ctx(ctx).Debug().Int("remaining_attempts", attempts).Msg("Failed to perform API call")
			// Add some randomness to prevent creating a Thundering Herd
			jitter := time.Duration(rand.Int63n(int64(sleep)))
			sleep = sleep + jitter/2
			time.Sleep(sleep)
			return a.retryBackoff(ctx, attempts, RetryBackoffExponentialFactor*sleep, f)
		}
		return &ApiRetriesExceeded{
			ApiError: ApiError{
				Err:         err,
				Text:        fmt.Sprintf("Failed to perform operation after %d retries", maxAttempts),
				StatusCode:  0,
				RawData:     nil,
				ApiResponse: nil,
			},
			Retries: maxAttempts,
		}
	}
	return nil
}

// ApiResponse returned by Request method
type ApiResponse struct {
	Data           json.RawMessage `json:"data"` // Data, may be either object, dict or list
	ErrorCodes     []string        `json:"data.exceptionClass,omitempty"`
	Message        string          `json:"message,omitempty"`    // Optional, can have error message
	NextToken      uuid.UUID       `json:"next_token,omitempty"` // For paginated objects
	HttpStatusCode int
}

// ApiObject generic interface of API object of any type (FileSystem, Quota, etc.)
type ApiObject interface {
	GetType() string
	GetBasePath() string
	GetApiUrl() string
	EQ(other ApiObject) bool
	getImmutableFields() []string
	String() string
}

// ApiObjectRequest interface that describes a request for an ApiObject CRUD operation
type ApiObjectRequest interface {
	getRequiredFields() []string
	hasRequiredFields() bool
	getRelatedObject() ApiObject
	getApiUrl() string
	String() string
}

type Credentials struct {
	Username     string
	Password     string
	Organization string
	HttpScheme   string
	Endpoints    []string
}

func (c *Credentials) String() string {
	return fmt.Sprintf("%s://%s:%s@%s",
		c.HttpScheme, c.Username, c.Organization, c.Endpoints)
}
