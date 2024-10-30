package apiclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/showa-93/go-mask"
	"go.opentelemetry.io/otel"
	"golang.org/x/exp/maps"
	"hash/fnv"
	"io"
	"k8s.io/helm/pkg/urlutil"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type ApiUserRole string

const (
	ApiHttpTimeOutSeconds                     = 60
	ApiRetryIntervalSeconds                   = 1
	ApiRetryMaxCount                          = 5
	RetryBackoffExponentialFactor             = 1
	RootOrganizationName                      = "Root"
	TracerName                                = "weka-csi"
	ApiUserRoleClusterAdmin       ApiUserRole = "ClusterAdmin"
	ApiUserRoleOrgAdmin           ApiUserRole = "OrgAdmin"
	ApiUserRoleReadOnly           ApiUserRole = "ReadOnly"
	ApiUserRoleCSI                ApiUserRole = "CSI"
	ApiUserRoleS3                 ApiUserRole = "S3"
	ApiUserRoleRegular            ApiUserRole = "Regular"
)

// ApiClient is a structure that defines Weka API client
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
	actualApiEndpoints         map[string]*ApiEndPoint
	currentEndpoint            string
	apiToken                   string
	apiTokenExpiryDate         time.Time
	refreshToken               string
	apiTokenExpiryInterval     int64
	refreshTokenExpiryInterval int64
	refreshTokenExpiryDate     time.Time
	CompatibilityMap           *WekaCompatibilityMap
	clientHash                 uint32
	hostname                   string
	NfsInterfaceGroups         map[string]*InterfaceGroup
	ApiUserRole                ApiUserRole
	ApiOrgId                   int
}

type ApiEndPoint struct {
	IpAddress            string
	MgmtPort             int
	lastActive           time.Time
	failCount            int64
	timeoutCount         int64
	http400ErrCount      int64
	http401ErrCount      int64
	http404ErrCount      int64
	http409ErrCount      int64
	http500ErrCount      int64
	generalErrCount      int64
	transportErrCount    int64
	noRespCount          int64
	parseErrCount        int64
	requestCount         int64
	requestDurationTotal time.Duration
}

func (e *ApiEndPoint) String() string {
	return fmt.Sprintf("%s:%d", e.IpAddress, e.MgmtPort)
}

func NewApiClient(ctx context.Context, credentials Credentials, allowInsecureHttps bool, hostname string) (*ApiClient, error) {
	logger := log.Ctx(ctx)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: allowInsecureHttps},
	}
	useCustomCACert := credentials.CaCertificate != ""
	if useCustomCACert {
		var caCertPool *x509.CertPool
		if pool, err := x509.SystemCertPool(); err != nil {
			caCertPool = x509.NewCertPool()
		} else {
			caCertPool = pool
		}
		caCertPool.AppendCertsFromPEM([]byte(credentials.CaCertificate))
		tr.TLSClientConfig.RootCAs = caCertPool
	}

	a := &ApiClient{
		Mutex: sync.Mutex{},
		client: &http.Client{
			Transport:     tr,
			CheckRedirect: nil,
			Jar:           nil,
			Timeout:       ApiHttpTimeOutSeconds * time.Second,
		},
		ClusterGuid:        uuid.UUID{},
		Credentials:        credentials,
		CompatibilityMap:   &WekaCompatibilityMap{},
		hostname:           hostname,
		actualApiEndpoints: make(map[string]*ApiEndPoint),
		NfsInterfaceGroups: make(map[string]*InterfaceGroup),
	}
	a.resetDefaultEndpoints(ctx)

	logger.Trace().Bool("insecure_skip_verify", allowInsecureHttps).Bool("custom_ca_cert", useCustomCACert).Msg("Creating new API client")
	a.clientHash = a.generateHash()
	return a, nil
}

func isValidIPv6Address(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	return ip != nil && ip.To4() == nil && ip.To16() != nil
}

func isValidIPv4Address(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	return ip != nil && ip.To4() != nil
}

func isValidHostname(hostname string) bool {
	if len(hostname) > 253 {
		return false
	}

	// Regex to match the general structure of a hostname.
	// Each label must start and end with an alphanumeric character,
	// may contain hyphens, and be 1 to 63 characters long.
	hostnameRegex := regexp.MustCompile(`^(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9][a-zA-Z0-9-]{0,61}[a-zA-Z0-9]$`)

	return hostnameRegex.MatchString(hostname)
}

func (a *ApiClient) resetDefaultEndpoints(ctx context.Context) {
	actualEndPoints := make(map[string]*ApiEndPoint)
	for _, e := range a.Credentials.Endpoints {

		split := strings.Split(e, ":")
		ip := ""
		port := "14000" // default port

		// if there is a port number in the endpoint, use it
		if len(split) > 1 {
			port = split[len(split)-1]
			ip = strings.Join(split[:len(split)-1], ":")
		} else {
			ip = split[0]
		}

		if !isValidIPv4Address(ip) && !isValidIPv6Address(ip) && !isValidHostname(ip) {
			log.Ctx(ctx).Error().Str("ip", ip).Msg("Cannot determine a valid hostname, IPv4 or IPv6 address, skipping endpoint")
			continue
		}

		portNum, err := strconv.Atoi(port)
		if err != nil {
			log.Ctx(ctx).Error().Err(err).Str("port", port).Msg("Failed to parse port number, using default")
			portNum = 14000
		}
		endPoint := &ApiEndPoint{
			IpAddress:            ip,
			MgmtPort:             portNum,
			lastActive:           time.Now(),
			failCount:            0,
			requestCount:         0,
			requestDurationTotal: 0,
		}
		actualEndPoints[e] = endPoint
	}
	a.actualApiEndpoints = actualEndPoints
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

// UpdateApiEndpoints fetches current management IP addresses of the cluster
func (a *ApiClient) UpdateApiEndpoints(ctx context.Context) error {
	logger := log.Ctx(ctx)
	nodes := &[]WekaNode{}
	err := a.GetNodesByRole(ctx, NodeRoleManagement, nodes)
	if err != nil {
		return err
	}
	if len(*nodes) == 0 {
		logger.Error().Msg("No management nodes found, not updating endpoints")
		return errors.New("no management nodes found, could not update api endpoints")
	}

	newEndpoints := make(map[string]*ApiEndPoint)
	updateTime := time.Now()

	// Create a copy of all existing endpoints to swap without locking
	existingEndpoints := make(map[string]*ApiEndPoint)
	for k, v := range a.actualApiEndpoints {
		newEndPoint := *v
		existingEndpoints[k] = &newEndPoint
	}

	for _, n := range *nodes {
		// Make sure that only backends and not clients are added to the list
		if n.Mode == NodeModeBackend {
			for _, IpAddress := range n.Ips {
				endpointKey := fmt.Sprintf("%s:%d", IpAddress, n.MgmtPort)
				existingEndpoint, ok := existingEndpoints[endpointKey]
				if ok {
					logger.Debug().Str("endpoint", endpointKey).Msg("Updating existing API endpoint")
					existingEndpoint.lastActive = updateTime
					newEndpoints[endpointKey] = existingEndpoint
				} else {
					logger.Info().Str("endpoint", endpointKey).Msg("Adding new API endpoint")
					endpoint := &ApiEndPoint{IpAddress: IpAddress, MgmtPort: n.MgmtPort, lastActive: updateTime, failCount: 0, requestCount: 0, requestDurationTotal: 0}
					newEndpoints[endpointKey] = endpoint
				}
			}
		}
	}
	// prune endpoints which are not active anymore (not existing on cluster)
	for _, endpoint := range existingEndpoints {
		if endpoint.lastActive.Before(updateTime) {
			logger.Warn().Time("endpoint_last_active_time", endpoint.lastActive).Str("endpoint", endpoint.String()).Msg("Removing inactive API endpoint")
			delete(newEndpoints, endpoint.String())
		}
	}

	a.actualApiEndpoints = newEndpoints

	// always rotate endpoint to make sure we distribute load between different Weka Nodes
	a.rotateEndpoint(ctx)
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

// rotateEndpoint returns a random endpoint of the configured ones
func (a *ApiClient) rotateEndpoint(ctx context.Context) {
	logger := log.Ctx(ctx)
	if len(a.actualApiEndpoints) == 0 {
		a.resetDefaultEndpoints(ctx)
	}
	if len(a.actualApiEndpoints) == 0 {
		a.currentEndpoint = ""
		logger.Error().Msg("Failed to choose random endpoint, no endpoints exist")
		return
	}
	keys := reflect.ValueOf(a.actualApiEndpoints).MapKeys()
	key := keys[rand.Intn(len(keys))].String()
	logger.Debug().Str("new_endpoint", key).Str("previous_endpoint", a.currentEndpoint).Msg("Switched to new API endpoint")
	a.currentEndpoint = key
}

// getEndpoint returns last known endpoint to work against
func (a *ApiClient) getEndpoint(ctx context.Context) *ApiEndPoint {
	if a.currentEndpoint == "" {
		a.rotateEndpoint(ctx)
	}
	return a.actualApiEndpoints[a.currentEndpoint]
}

// getBaseUrl returns the full HTTP URL of the API endpoint including schema, chosen endpoint and API prefix
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
	endpoint := a.getEndpoint(ctx)
	return fmt.Sprintf("%s://%s:%d/api/v2", scheme, endpoint.IpAddress, endpoint.MgmtPort)
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

	logger.Trace().Str("method", Method).Str("url", r.URL.RequestURI()).Str("payload", maskPayload(payload)).Msg("")

	//perform the request and update endpoint with stats
	endpoint := a.getEndpoint(ctx)
	endpoint.requestCount++
	start := time.Now()
	response, err := a.client.Do(r)

	if err != nil {
		endpoint.transportErrCount++
		return nil, &transportError{err}
	}

	if response == nil {
		endpoint.noRespCount++
		return nil, &transportError{errors.New("received no response")}
	}

	// update endpoint stats for success and total duration
	endpoint.requestDurationTotal += time.Since(start)
	if response.StatusCode != http.StatusOK {
		endpoint.failCount++
	}

	responseBody, err := io.ReadAll(response.Body)
	logger.Trace().Str("response", maskPayload(string(responseBody))).Msg("")
	if err != nil {
		endpoint.parseErrCount++
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
	endpoint.parseErrCount++
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
		endpoint.http400ErrCount++
		return Response, &ApiBadRequestError{
			Err:         nil,
			Text:        "Operation failed",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusUnauthorized: //401
		endpoint.http401ErrCount++
		return Response, &ApiAuthorizationError{
			Err:         nil,
			Text:        "Operation failed",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusNotFound: //404
		endpoint.http404ErrCount++
		return Response, &ApiNotFoundError{
			Err:         nil,
			Text:        "Object not found",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusConflict: //409
		endpoint.http409ErrCount++
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
		endpoint.http500ErrCount++
		return Response, ApiInternalError{
			Err:         nil,
			Text:        Response.Message,
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}

	default:
		endpoint.generalErrCount++
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
	op := "ApiClientRequest"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	err := a.retryBackoff(ctx, ApiRetryMaxCount, time.Second*time.Duration(ApiRetryIntervalSeconds), func() apiError {
		rawResponse, reqErr := a.do(ctx, Method, Path, Payload, Query)
		if a.handleNetworkErrors(ctx, reqErr) != nil { // transient network errors
			a.rotateEndpoint(ctx)
			logger.Error().Err(reqErr).Msg("")
			return reqErr
		}
		if reqErr != nil {
			return ApiNonTransientError{reqErr}
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
			return ApiNonTransientError{
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
			return ApiNonTransientError{reqErr}
		default:
			logger.Warn().Err(reqErr).Int("http_code", s).Msg("Failed to perform a request, got an unhandled error")
			return ApiNonTransientError{reqErr}
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

// generateHash used for storing multiple clients in hash table. Hash() is created once as connection params might change
func (a *ApiClient) generateHash() uint32 {
	h := fnv.New32a()
	s := fmt.Sprintln(
		a.Credentials.Username,
		a.Credentials.Password,
		a.Credentials.Organization,
		a.Credentials.Endpoints,
		a.Credentials.NfsTargetIPs,
		a.Credentials.LocalContainerName,
		a.Credentials.CaCertificate,
	)
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

// retryBackoff performs operation and retries on transient failures. Does not retry on ApiNonTransientError
func (a *ApiClient) retryBackoff(ctx context.Context, attempts int, sleep time.Duration, f func() apiError) error {
	maxAttempts := attempts
	if err := f(); err != nil {
		switch s := err.(type) {
		case ApiNonTransientError:
			log.Ctx(ctx).Trace().Msg("Non-transient error returned from API, stopping further attempts")
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
	GetType() string                 // returns the type of the object
	GetBasePath(a *ApiClient) string // returns the base path of objects of this type (plural)
	GetApiUrl(a *ApiClient) string   // returns the full URL of the object consisting of base path and object UID
	EQ(other ApiObject) bool         // a way to compare objects and check if they are the same
	getImmutableFields() []string    // provides a list of fields that are used for comparison in EQ()
	String() string                  // returns a string representation of the object
}

// ApiObjectRequest interface that describes a request for an ApiObject CRUD operation
type ApiObjectRequest interface {
	getRequiredFields() []string   // returns a list of fields that are mandatory for the object for creation
	hasRequiredFields() bool       // checks if all mandatory fields are filled in
	getRelatedObject() ApiObject   // returns the type of object that is being requested
	getApiUrl(a *ApiClient) string // returns the full URL of the object consisting of base path and object UID
	String() string                // returns a string representation of the object request
}

type Credentials struct {
	Username            string
	Password            string
	Organization        string
	HttpScheme          string
	Endpoints           []string
	LocalContainerName  string
	AutoUpdateEndpoints bool
	CaCertificate       string
	NfsTargetIPs        []string
}

func (c *Credentials) String() string {
	return fmt.Sprintf("%s://%s:%s@%s",
		c.HttpScheme, c.Username, c.Organization, c.Endpoints)
}

func (a *ApiClient) HasCSIPermissions() bool {
	if a.ApiUserRole != "" {
		return a.ApiUserRole == ApiUserRoleCSI || a.ApiUserRole == ApiUserRoleClusterAdmin || a.ApiUserRole == ApiUserRoleOrgAdmin
	}
	return false
}

func maskPayload(payload string) string {
	masker := mask.NewMasker()
	masker.RegisterMaskStringFunc(mask.MaskTypeFilled, masker.MaskFilledString)
	masker.RegisterMaskField("username", "filled4")
	masker.RegisterMaskField("password", "filled4")
	masker.RegisterMaskField("access_token", "filled4")
	masker.RegisterMaskField("mount_token", "filled4")
	masker.RegisterMaskField("refresh_token", "filled4")
	var target any
	err := json.Unmarshal([]byte(payload), &target)
	if err != nil {
		return payload
	}
	masked, _ := masker.Mask(target)
	ret, _ := json.Marshal(masked)
	return string(ret)
}
