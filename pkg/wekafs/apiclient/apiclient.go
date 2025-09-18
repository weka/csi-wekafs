package apiclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/helm/pkg/urlutil"
)

type ApiUserRole string

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
	client                      *http.Client
	Credentials                 Credentials
	ClusterGuid                 uuid.UUID
	ClusterName                 string
	MountEndpoints              []string
	apiEndpoints                *ApiEndPoints
	apiToken                    string
	apiTokenExpiryDate          time.Time
	refreshToken                string
	apiTokenExpiryInterval      int64
	refreshTokenExpiryInterval  int64
	refreshTokenExpiryDate      time.Time
	CompatibilityMap            *WekaCompatibilityMap
	clientHash                  uint32
	hostname                    string
	NfsInterfaceGroups          map[string]*InterfaceGroup
	ApiUserRole                 ApiUserRole
	ApiOrgId                    int
	containerName               string
	NfsInterfaceGroupName       string
	NfsClientGroupName          string
	metrics                     *ApiMetrics
	driverName                  string
	RotateEndpointOnEachRequest bool // to be used in metrics server only (atm) to increase concurrency of requests across endpoints

	containers           *ContainersResponse
	containersUpdateTime time.Time
	containersLock       sync.RWMutex

	fsCache   map[string]*fsCacheEntry
	fsCacheMu sync.RWMutex
}

type ApiClientOptions struct {
	AllowInsecureHttps bool
	Hostname           string
	DriverName         string
	ApiTimeout         time.Duration
}

func NewApiClient(ctx context.Context, credentials Credentials, opts ApiClientOptions) (*ApiClient, error) {
	logger := log.Ctx(ctx)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: opts.AllowInsecureHttps},
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
			Timeout:       opts.ApiTimeout,
		},
		ClusterGuid:        uuid.UUID{},
		Credentials:        credentials,
		CompatibilityMap:   &WekaCompatibilityMap{},
		hostname:           opts.Hostname,
		apiEndpoints:       NewApiEndPoints(),
		NfsInterfaceGroups: make(map[string]*InterfaceGroup),
	}
	err := a.resetDefaultEndpoints(ctx)
	if err != nil || len(a.Credentials.Endpoints) < 1 {
		return nil, &ApiNoEndpointsError{
			Err: status.Errorf(codes.Unavailable, "No endpoints available %v", err),
		}
	}

	logger.Trace().Bool("insecure_skip_verify", opts.AllowInsecureHttps).Bool("custom_ca_cert", useCustomCACert).Msg("Creating new API client")
	return a, nil
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

// handleTransientErrors checks if the error returned by endpoint is a network error (transient by definition)
func (a *ApiClient) handleTransientErrors(ctx context.Context, err error) error {
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

		case *transportError:
			if t.Err != nil {
				return &ApiNetworkError{Err: errors.New(fmt.Sprintln("Transport error:", a.getEndpoint(ctx), t.Err))}
			}
		case *ApiNotAvailableError:
			return &ApiNetworkError{Err: errors.New(fmt.Sprintln("Service unavailable:", a.getEndpoint(ctx), t.Err))}
		default:
			return nil
		}
	}
	// In this case this is not a network error, will be treated separately
	return nil
}

// getUrl returns a URL which consists of baseUrl + path
func (a *ApiClient) getUrl(ctx context.Context, path string) string {
	u, _ := urlutil.URLJoin(a.getBaseUrl(ctx), path)
	return u
}

// retryBackoff performs operation and retries on transient failures. Does not retry on ApiNonTransientError
func (a *ApiClient) retryBackoff(ctx context.Context, attempts int, sleep time.Duration, f func() apiError) error {
	maxAttempts := attempts
	if err := f(); err != nil {
		switch s := err.(type) {
		case ApiResponseNextPage:
			return s // This is not an error, just a signal to continue with the next page
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
