package apiclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"hash/fnv"
	"k8s.io/helm/pkg/urlutil"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"
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
	containerName              string
	NfsInterfaceGroupName      string
	NfsClientGroupName         string

	containers           *ContainersResponse
	containersUpdateTime time.Time
	containersLock       sync.RWMutex
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
	if len(a.Credentials.Endpoints) < 1 {
		return nil, &ApiNoEndpointsError{
			Err: errors.New("no endpoints could be found for API client"),
		}
	}

	logger.Trace().Bool("insecure_skip_verify", allowInsecureHttps).Bool("custom_ca_cert", useCustomCACert).Msg("Creating new API client")
	a.clientHash = a.generateHash()
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
