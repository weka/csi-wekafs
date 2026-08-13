package apiclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
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
	// Guards the mutable state below: Login writes it while concurrent requests read it.
	sync.RWMutex
	// loginMu serialises login attempts. Kept separate from RWMutex so a login, which makes several
	// round-trips, never holds the state lock across I/O.
	loginMu            sync.Mutex
	client             *http.Client
	Credentials        Credentials
	ClusterGuid        uuid.UUID
	ClusterName        string
	MountEndpoints     []string
	apiEndpoints       *ApiEndPoints
	apiToken           string
	apiTokenExpiryDate time.Time
	// loginComplete is true only once a login has published its token *and* finished the post-auth
	// setup that follows (permission check, cluster info, endpoint discovery). Login clears it the
	// moment it publishes a new token and sets it only on full success, so a goroutine that was
	// blocked on loginMu while another goroutine's setup failed observes the failure - via
	// loginSucceeded - instead of a token that merely exists.
	loginComplete              bool
	refreshToken               string
	apiTokenExpiryInterval     int64
	refreshTokenExpiryInterval int64
	refreshTokenExpiryDate     time.Time
	CompatibilityMap           *WekaCompatibilityMap
	clientHash                 uint32
	hostname                   string
	driverName                 string
	// nfsInterfaceGroups caches interface groups by name. It carries its own lock - see the type.
	nfsInterfaceGroups    *interfaceGroups
	ApiUserRole           ApiUserRole
	ApiOrgId              int
	containerName         string
	NfsInterfaceGroupName string
	NfsClientGroupName    string

	containers           *ContainersResponse
	containersUpdateTime time.Time
	containersLock       sync.RWMutex

	// fsCache memoises filesystem lookups by name. Callers choose how stale an answer they will
	// accept, so a caller needing certainty can still bypass it via GetFileSystemByName.
	fsCache   map[string]*fsCacheEntry
	fsCacheMu sync.RWMutex
}

// ApiClientOptions carries the settings NewApiClient needs beyond the credentials themselves.
// Grouping them keeps the constructor from growing another positional parameter each time a
// setting is added.
type ApiClientOptions struct {
	// AllowInsecureHttps skips TLS certificate verification.
	AllowInsecureHttps bool
	// Hostname identifies this client to the Weka cluster.
	Hostname string
	// DriverName labels the metrics this client records, so several drivers scraped together stay
	// distinguishable.
	DriverName string
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
		client: &http.Client{
			Transport:     tr,
			CheckRedirect: nil,
			Jar:           nil,
			Timeout:       ApiHttpTimeOutSeconds * time.Second,
		},
		ClusterGuid:        uuid.UUID{},
		Credentials:        credentials,
		CompatibilityMap:   &WekaCompatibilityMap{},
		hostname:           opts.Hostname,
		driverName:         opts.DriverName,
		apiEndpoints:       NewApiEndPoints(),
		nfsInterfaceGroups: newInterfaceGroups(),
	}
	if len(a.Credentials.Endpoints) < 1 {
		return nil, &ApiNoEndpointsError{
			Err: errors.New("no endpoints could be found for API client"),
		}
	}
	if err := a.resetDefaultEndpoints(ctx); err != nil {
		return nil, &ApiNoEndpointsError{Err: err}
	}

	logger.Trace().Bool("insecure_skip_verify", opts.AllowInsecureHttps).Bool("custom_ca_cert", useCustomCACert).Msg("Creating new API client")
	a.fsCache = make(map[string]*fsCacheEntry)
	a.clientHash = a.generateHash()
	return a, nil
}

// getBaseUrl returns the full HTTP URL of the API endpoint including schema, chosen endpoint and API
// prefix. It reports ApiNoEndpointsError, via requireEndpoint, rather than dereferencing a nil
// endpoint when none is known.
func (a *ApiClient) getBaseUrl(ctx context.Context) (string, apiError) {
	scheme := ""
	switch strings.ToUpper(a.Credentials.HttpScheme) {

	case "HTTP":
		scheme = "http"
	case "HTTPS":
		scheme = "https"
	default:
		scheme = "http"
	}
	endpoint, err := a.requireEndpoint(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s://%s:%d/api/v2", scheme, endpoint.IpAddress, endpoint.MgmtPort), nil
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
func (a *ApiClient) getUrl(ctx context.Context, path string) (string, apiError) {
	base, err := a.getBaseUrl(ctx)
	if err != nil {
		return "", err
	}
	u, _ := url.JoinPath(base, path)
	return u, nil
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
		a.Credentials.KmsPreexistingCredentialsForVolumeEncryption.InsecureString(),
		a.Credentials.KmsKeyManagementCredentials.InsecureString(),
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
			// A full task queue drains only as tasks finish, so back off harder for it than for an
			// ordinary transient error, which usually clears immediately.
			factor := RetryBackoffExponentialFactor
			if IsTooManyTasksError(err) {
				factor = RetryBackoffTooManyTasksFactor
			}
			// Add some randomness to prevent creating a Thundering Herd
			jitter := time.Duration(rand.Int63n(int64(sleep)))
			sleep = sleep + jitter/2
			maxSleep := time.Second * MaxRetryBackoffTooManyTasksSeconds
			if sleep > maxSleep {
				sleep = maxSleep
			}
			time.Sleep(sleep)
			return a.retryBackoff(ctx, attempts, time.Duration(factor)*sleep, f)
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
