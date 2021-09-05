package apiclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/golang/glog"
	"github.com/google/uuid"
	"github.com/hashicorp/go-version"
	"hash/fnv"
	"io/ioutil"
	"k8s.io/helm/pkg/urlutil"
	"math/rand"
	"net"
	"net/http"
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
)

//ApiClient is a structure that defines Weka API client
// client: http.Client ref
// Username, Password - obvious
// httpScheme: either 'http', 'https'
// Endpoints: slice of 'ip_address_or_fqdn:port' strings
// apiToken, refreshToken, apiTokenExpiryDate used for bearer auth
// currentEndpointId: refers to the currently working API endpoint
// Timeout sets max request timeout duration
type ApiClient struct {
	sync.Mutex
	client                     *http.Client
	ClusterGuid                uuid.UUID
	ClusterName                string
	Username                   string
	Password                   string
	Organization               string
	httpScheme                 string
	Endpoints                  []string
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

type WekaCompatibilityRequiredVersions struct {
	FilesystemAsVolume     string
	DirectoryAsVolume      string
	QuotaDirectoryAsVolume string
	QuotaOnNonEmptyDirs    string
}

var MinimumSupportedWekaVersions = &WekaCompatibilityRequiredVersions{
	DirectoryAsVolume:      "v3.0",  // can create CSI volume from directory, without quota support
	FilesystemAsVolume:     "v3.13", // can create CSI volume from filesystem
	QuotaDirectoryAsVolume: "v3.13", // can create CSI volume from directory with quota support
	QuotaOnNonEmptyDirs:    "v3.99", // can enable quota on legacy CSI volume (directory) without quota support
}

type WekaCompatibilityMap struct {
	FilesystemAsVolume     bool
	DirectoryAsVolume      bool
	QuotaDirectoryAsVolume bool
	QuotaOnNonEmptyDirs    bool
}

func (cm *WekaCompatibilityMap) fillIn(versionStr string) {
	v, err := version.NewVersion(versionStr)
	if err != nil {
		glog.Errorln("Could not parse cluster version", versionStr, "assuming new features are unsupported!")
		cm.DirectoryAsVolume = true
		cm.FilesystemAsVolume = false
		cm.QuotaDirectoryAsVolume = false
		cm.QuotaOnNonEmptyDirs = false
		return
	}
	d, err := version.NewVersion(MinimumSupportedWekaVersions.DirectoryAsVolume)
	f, err := version.NewVersion(MinimumSupportedWekaVersions.FilesystemAsVolume)
	q, err := version.NewVersion(MinimumSupportedWekaVersions.QuotaDirectoryAsVolume)
	n, err := version.NewVersion(MinimumSupportedWekaVersions.QuotaOnNonEmptyDirs)

	cm.DirectoryAsVolume = v.GreaterThanOrEqual(d)
	cm.FilesystemAsVolume = v.GreaterThanOrEqual(f)
	cm.QuotaDirectoryAsVolume = v.GreaterThanOrEqual(q)
	cm.QuotaOnNonEmptyDirs = v.GreaterThanOrEqual(n)
}

func NewApiClient(username, password, organization string, endpoints []string, scheme string) (*ApiClient, error) {
	a := &ApiClient{
		Mutex: sync.Mutex{},
		client: &http.Client{
			Transport:     nil,
			CheckRedirect: nil,
			Jar:           nil,
			Timeout:       0,
		},
		ClusterGuid:       uuid.UUID{},
		Username:          username,
		Password:          password,
		Organization:      organization,
		httpScheme:        scheme,
		Endpoints:         endpoints,
		CompatibilityMap:  &WekaCompatibilityMap{},
		Timeout:           time.Duration(ApiHttpTimeOutSeconds) * time.Second,
		currentEndpointId: -1,
	}
	a.Log(3, "Creating new API client for endpoints", endpoints)
	a.clientHash = a.generateHash()
	return a, nil
}

// fetchMountEndpoints used to obtain actual data plane IP addresses
func (a *ApiClient) fetchMountEndpoints() error {
	f := a.Log(4, "Fetching mount points")
	defer f()
	a.MountEndpoints = []string{}
	nodes := &[]WekaNode{}
	err := a.GetNodesByRole(NodeRoleBackend, nodes)
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
	a.Endpoints = endpoints
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

//chooseRandomEndpoint returns a random endpoint of the configured ones
func (a *ApiClient) chooseRandomEndpoint() {
	if a.Endpoints == nil || len(a.Endpoints) == 0 {
		a.currentEndpointId = -1
		a.Log(3, "Failed to choose random endpoint, no endpoints exist")
		return
	}
	a.currentEndpointId = rand.Intn(len(a.Endpoints))
	a.Log(4, "Choosing random endpoint", a.getEndpoint())
}

//getEndpoint returns last known endpoint to work against
func (a *ApiClient) getEndpoint() string {
	if a.currentEndpointId < 0 {
		a.chooseRandomEndpoint()
	}
	return a.Endpoints[a.currentEndpointId]
}

//getBaseUrl returns the full HTTP URL of the API endpoint including schema, chosen endpoint and API prefix
func (a *ApiClient) getBaseUrl() string {
	scheme := ""
	switch strings.ToUpper(a.httpScheme) {

	case "HTTP":
		scheme = "http"
	case "HTTPS":
		scheme = "https"
	default:
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s/api/v2", scheme, a.getEndpoint())
}

// do Makes a basic API call to the client, returns an *ApiResponse that includes raw data, error message etc.
func (a *ApiClient) do(Method string, Path string, Payload *[]byte, Query *map[string]string) (*ApiResponse, apiError) {
	//construct URL path
	if len(a.Endpoints) < 1 {
		return &ApiResponse{}, &ApiNoEndpointsError{
			Err: errors.New("no endpoints could be found for API client"),
		}
	}
	url := a.getUrl(Path)

	//construct base request and add auth if exists
	var body *bytes.Reader
	if Payload != nil {
		body = bytes.NewReader(*Payload)
	} else {
		body = bytes.NewReader([]byte(""))
	}
	r, err := http.NewRequest(Method, url, body)
	if err != nil {
		return nil, &ApiError{
			Err:         err,
			Text:        "Failed to construct API request",
			StatusCode:  nil,
			RawData:     nil,
			ApiResponse: nil,
		}
	}
	r.Header.Set("content-type", "application/json")
	if a.isLoggedIn() {
		r.Header.Set("Authorization", fmt.Sprintf("Bearer %s", a.apiToken))
	}

	//add query params
	if Query != nil {
		q := r.URL.Query()
		for k, v := range *Query {
			q.Add(k, v)
		}
		r.URL.RawQuery = q.Encode()
	}

	a.Log(5, Method, r.URL.RequestURI(), func() string {
		if Payload != nil {
			return string(*Payload)
		}
		return "<no-payload>"
	}(),
	)
	response, err := a.client.Do(r)
	if err != nil {
		return nil, &ApiError{
			Err:         err,
			Text:        "Request failed",
			StatusCode:  nil,
			RawData:     nil,
			ApiResponse: nil,
		}
	}
	responseBody, err := ioutil.ReadAll(response.Body)
	a.Log(5, string(responseBody))
	if err != nil {
		return nil, &ApiError{
			Err:         err,
			Text:        "Failed to read from request",
			StatusCode:  &response.StatusCode,
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
		a.Log(2, fmt.Sprintf("Could not response parse json, HTTP status code %d, %s", Response.HttpStatusCode, err.Error()))
		return nil, &ApiError{
			Err:         err,
			Text:        "Failed to parse HTTP response body",
			StatusCode:  &response.StatusCode,
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
			StatusCode:  &response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusUnauthorized: //401
		return Response, &ApiAuthorizationError{
			Err:         nil,
			Text:        "Operation failed",
			StatusCode:  &response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusNotFound: //404
		return Response, &ApiNotFoundError{
			Err:         nil,
			Text:        "Object not found",
			StatusCode:  &response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusConflict: //409
		return Response, &ApiConflictError{
			ApiError: ApiError{
				Err:         nil,
				Text:        "Object conflict",
				StatusCode:  &response.StatusCode,
				RawData:     &responseBody,
				ApiResponse: Response,
			},
			ConflictingEntityId: nil, //TODO: parse and provide entity ID when supplied by API
		}

	case http.StatusInternalServerError: //500
		return Response, ApiInternalError{
			Err:         nil,
			Text:        Response.Message,
			StatusCode:  &response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}

	default:
		return Response, &ApiError{
			Err:         err,
			Text:        "General failure during API command",
			StatusCode:  &response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	}
}

// handleNetworkErrors checks if the error returned by endpoint is a network error (transient by definition)
func (a *ApiClient) handleNetworkErrors(err error) error {
	if err == nil {
		return nil
	}
	if netError, ok := err.(net.Error); ok && netError.Timeout() {
		return &ApiNetworkError{Err: errors.New(fmt.Sprintln("Connection timed out to ", a.getEndpoint()))}
	} else {
		switch t := err.(type) {
		case *net.OpError:
			if t.Op == "dial" {
				return &ApiNetworkError{Err: errors.New(fmt.Sprintln("Unknown host", a.getEndpoint()))}
			} else if t.Op == "read" {
				return &ApiNetworkError{Err: errors.New(fmt.Sprintln("Connection refused:", a.getEndpoint()))}
			}

		case syscall.Errno:
			if t == syscall.ECONNREFUSED {
				return &ApiNetworkError{Err: errors.New(fmt.Sprintln("Connection refused:", a.getEndpoint()))}
			}
		}
	}
	// In this case this is not a network error, will be treated separately
	return nil
}

// request wraps do with retries and some more error handling
func (a *ApiClient) request(Method string, Path string, Payload *[]byte, Query *map[string]string, v interface{}) apiError {
	f := a.Log(5, "Performing request:", Method, Path)
	defer f()
	err := retryBackoff(ApiRetryMaxCount, time.Second*time.Duration(ApiRetryIntervalSeconds), func() apiError {
		rawResponse, reqErr := a.do(Method, Path, Payload, Query)
		if a.handleNetworkErrors(reqErr) != nil { // transient network errors
			a.Log(2, "Failed to perform request, error received:", reqErr.Error())
			a.chooseRandomEndpoint()
			return reqErr
		}
		s := rawResponse.HttpStatusCode
		var responseCodes []string
		if len(rawResponse.ErrorCodes) > 0 {
			a.Log(1, "Failed to execute request, got codes", rawResponse.ErrorCodes)
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
			a.Log(2, "Could not parse JSON request", reflect.TypeOf(v), err)
		}
		switch s {
		case http.StatusOK:
			return nil
		case http.StatusUnauthorized:
			a.Log(4, "Got Authorization failure on request, trying to re-login")
			_ = a.Init()
			return reqErr
		case http.StatusNotFound, http.StatusConflict, http.StatusBadRequest, http.StatusInternalServerError:
			return ApiNonrecoverableError{reqErr}
		default:
			a.Log(2, "Failed to perform a request, got an unhandled error", reqErr, s)
			return ApiNonrecoverableError{reqErr}
		}
	})
	if err != nil {
		return err.(apiError)
	}
	return nil
}

// Request makes sure that client is logged in and has a non-expired token
func (a *ApiClient) Request(Method string, Path string, Payload *[]byte, Query *map[string]string, Response interface{}) error {
	if err := a.Init(); err != nil {
		a.Log(1, fmt.Sprintf("Failed to perform request since failed to re-authenticate: %s", err.Error()))
	}
	err := a.request(Method, Path, Payload, Query, Response)
	if err != nil {
		return err
	}
	return nil
}

// Get is shortcut for Request("GET" ...)
func (a *ApiClient) Get(Path string, Query *map[string]string, Response interface{}) error {
	return a.Request("GET", Path, nil, Query, Response)
}

// Post is shortcut for Request("POST" ...)
func (a *ApiClient) Post(Path string, Payload *[]byte, Query *map[string]string, Response interface{}) error {
	return a.Request("POST", Path, Payload, Query, Response)
}

// Put is shortcut for Request("PUT" ...)
func (a *ApiClient) Put(Path string, Payload *[]byte, Query *map[string]string, Response interface{}) error {
	return a.Request("PUT", Path, Payload, Query, Response)
}

// Delete is shortcut for Request("DELETE" ...)
func (a *ApiClient) Delete(Path string, Payload *[]byte, Query *map[string]string, Response interface{}) error {
	return a.Request("DELETE", Path, Payload, Query, Response)
}

// getUrl returns a URL which consists of baseUrl + path
func (a *ApiClient) getUrl(path string) string {
	url, _ := urlutil.URLJoin(a.getBaseUrl(), path)
	return url
}

// Login logs into API, updates refresh token expiry
func (a *ApiClient) Login() error {
	if a.isLoggedIn() {
		return nil
	}
	a.Lock()
	defer a.Unlock()
	f := a.Log(2, "Logging in to endpoint", a.getEndpoint())
	defer f()

	r := LoginRequest{
		Username: a.Username,
		Password: a.Password,
		Org:      a.Organization,
	}
	jb, err := marshalRequest(r)
	if err != nil {
		return err
	}
	responseData := &LoginResponse{}
	if err := a.request("POST", ApiPathLogin, jb, nil, responseData); err != nil {
		if err.getType() == "ApiAuthorizationError" {
			glog.Errorf("Could not log in to endpoint %s", a.getEndpoint())
		}
		return err
	}
	a.apiToken = responseData.AccessToken
	a.refreshToken = responseData.RefreshToken
	a.apiTokenExpiryDate = time.Now().Add(time.Duration(responseData.ExpiresIn-30) * time.Second)
	if a.refreshTokenExpiryInterval < 1 {
		_ = a.updateTokensExpiryInterval()
	}
	a.refreshTokenExpiryDate = time.Now().Add(time.Duration(a.refreshTokenExpiryInterval) * time.Second)
	_ = a.fetchClusterInfo()
	a.Log(2, "successfully connected to cluster API")
	return nil
}

func (a *ApiClient) Log(level glog.Level, message ...interface{}) func() {
	glog.V(level).Infoln(fmt.Sprintf("API client: %s (%s)", a.ClusterName, a.ClusterGuid.String()), message)
	return func() {
		glog.V(level).Infoln(fmt.Sprintf("API client: %s (%s)", a.ClusterName, a.ClusterGuid.String()), message, "completed")
	}
}

// generateHash used for storing multiple clients in hash table. Hash() is created once as connection params might change
func (a *ApiClient) generateHash() uint32 {
	a.Log(5, "Generating API hash")
	h := fnv.New32a()
	s := fmt.Sprintln(a.Username, a.Password, a.Organization, a.Endpoints)
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// Hash returns the client hash as it was generated once client was initialized
func (a *ApiClient) Hash() uint32 {
	return a.clientHash
}

// Init checks if API token refresh is required and transparently refreshes or fails back to (re)login
func (a *ApiClient) Init() error {
	if a.apiTokenExpiryDate.After(time.Now()) {
		a.Log(5, "Authentication token is valid for", a.apiTokenExpiryDate.Sub(time.Now()))
		return nil
	}
	if !a.isLoggedIn() {
		a.Log(3, "Client is not authenticated, logging in...")
		return a.Login()
	}

	a.Log(5, "Performing Bearer token refresh")
	r := RefreshRequest{RefreshToken: a.refreshToken}
	responseData := &RefreshResponse{}
	payload, _ := marshalRequest(r)
	if err := a.request("POST", ApiPathRefresh, payload, nil, responseData); err != nil {
		a.Log(4, "Failed to refresh auth token, logging in...")
		return a.Login()
	}
	a.refreshToken = responseData.RefreshToken
	a.apiToken = responseData.AccessToken
	a.apiTokenExpiryDate = time.Now().Add(time.Duration(a.apiTokenExpiryInterval-30) * time.Second)
	a.Log(5, "Authentication token refreshed successfully, valid for", a.apiTokenExpiryDate.Sub(time.Now()))
	return nil
}

func (a *ApiClient) SupportsQuotaDirectoryAsVolume() bool {
	return a.CompatibilityMap.QuotaDirectoryAsVolume
}
func (a *ApiClient) SupportsQuotaOnNonEmptyDirs() bool {
	return a.CompatibilityMap.QuotaOnNonEmptyDirs
}

func (a *ApiClient) SupportsFilesystemAsVolume() bool {
	return a.CompatibilityMap.FilesystemAsVolume
}
func (a *ApiClient) SupportsDirectoryAsVolume() bool {
	return a.CompatibilityMap.DirectoryAsVolume
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
func retryBackoff(attempts int, sleep time.Duration, f func() apiError) error {
	maxAttempts := attempts
	if err := f(); err != nil {
		switch s := err.(type) {
		case ApiNonrecoverableError:
			glog.V(6).Infoln("Non-recoverable error occurred, stopping further attempts")
			// Return the original error for later checking
			return s.apiError
		}
		if attempts--; attempts > 0 {
			glog.V(3).Infof("Failed to perform API call, %d attempts left", attempts)
			// Add some randomness to prevent creating a Thundering Herd
			jitter := time.Duration(rand.Int63n(int64(sleep)))
			sleep = sleep + jitter/2
			time.Sleep(sleep)
			return retryBackoff(attempts, RetryBackoffExponentialFactor*sleep, f)
		}
		return &ApiRetriesExceeded{
			ApiError: ApiError{
				Err:         err,
				Text:        fmt.Sprintf("Failed to perform operation after %d retries", maxAttempts),
				StatusCode:  nil,
				RawData:     nil,
				ApiResponse: nil,
			},
			Retries: maxAttempts,
		}
	}
	return nil
}

// ApiNonrecoverableError is internally generated when non-transient error is found
type ApiNonrecoverableError struct {
	apiError
}

func (e ApiNonrecoverableError) Error() string {
	return e.apiError.Error()
}
func (e ApiNonrecoverableError) getType() string {
	return "ApiNonrecoverableError"
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

// ObjectsAreEqual returns true if both ApiObject have same immutable fields (other fields are disregarded)
func ObjectsAreEqual(o1 ApiObject, o2 ApiObject) bool {
	if reflect.TypeOf(o1) != reflect.TypeOf(o2) {
		return false
	}
	ref := reflect.ValueOf(o1)
	oth := reflect.ValueOf(o2)
	for _, field := range o1.getImmutableFields() {
		qval := reflect.Indirect(ref).FieldByName(field)
		val := reflect.Indirect(oth).FieldByName(field)
		if !qval.IsZero() {
			if !reflect.DeepEqual(qval.Interface(), val.Interface()) {
				return false
			}
		}
	}
	return true
}

// ObjectRequestHasRequiredFields returns true if all mandatory fields of object in API request are filled in
func ObjectRequestHasRequiredFields(o ApiObjectRequest) bool {
	ref := reflect.ValueOf(o)
	var missingFields []string
	for _, field := range o.getRequiredFields() {
		if reflect.Indirect(ref).FieldByName(field).IsZero() {
			missingFields = append(missingFields, field)
		}
	}
	if len(missingFields) > 0 {
		glog.Errorln("Object is missing the following fields:", missingFields)
		return false
	}
	return true
}
