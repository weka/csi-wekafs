package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// quietLogs silences the client's per-request trace logging for the duration of a test. These tests
// issue hundreds of requests, and what is under test is the race detector's verdict, not the output.
// The level is restored afterwards so other tests in the package are unaffected.
func quietLogs(t *testing.T) {
	t.Helper()
	previous := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.Disabled)
	t.Cleanup(func() { zerolog.SetGlobalLevel(previous) })
}

// This file exercises ApiClient under `go test -race` to catch data races in its concurrency
// primitives (the RWMutex-guarded auth state, the ApiEndPoints map, and per-endpoint counters).
// It stands up a fake Weka REST API with httptest and never touches a real cluster or
// localhost:14000.

// wrapData marshals v and wraps it the way the real Weka API wraps every response: as the "data"
// field of an envelope. ApiClient's do()/request() unwrap it the same way on the way back in.
func wrapData(v interface{}) ([]byte, error) {
	inner, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal test fixture: %w", err)
	}
	out, err := json.Marshal(struct {
		Data json.RawMessage `json:"data"`
	}{Data: inner})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal test envelope: %w", err)
	}
	return out, nil
}

// writeJSON wraps v via wrapData and writes it as the HTTP response body. It runs on the handler's
// own goroutine (mux.HandleFunc), not the test goroutine, so a marshal failure must not call
// t.Fatalf: that invokes runtime.Goexit on this goroutine, which aborts the response mid-flight and
// surfaces to the client as an opaque EOF while the test can still report a pass. t.Errorf plus a
// 500 response instead fails the test and gives the client a real error to react to.
func writeJSON(t *testing.T, w http.ResponseWriter, v interface{}) {
	t.Helper()
	data, err := wrapData(v)
	if err != nil {
		t.Errorf("%v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// newRaceTestServer stands up an httptest server that emulates just enough of the Weka REST API
// for ApiClient to log in, refresh tokens, and rediscover its management endpoints repeatedly.
//
// The /api/v2/nodes handler (backing UpdateApiEndpoints) returns a different set of management
// endpoints on every call, driven by an atomic counter, while keeping every reported IP a
// genuinely reachable alias of this same server ("127.0.0.1" and "localhost" both resolve to the
// loopback address httptest.NewServer listens on). That means ApiClient keeps working across
// repeated logins while its internal endpoint map is genuinely replaced each time - the condition
// that would surface a "concurrent map read and map write" race.
func newRaceTestServer(t *testing.T) (hostPort string, mgmtPort int) {
	t.Helper()
	var endpointGen int32

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v2/login", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, LoginResponse{
			AccessToken:  "access-token",
			TokenType:    "bearer",
			ExpiresIn:    3600,
			RefreshToken: "refresh-token",
		})
	})

	mux.HandleFunc("/api/v2/login/refresh", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, RefreshResponse{
			AccessToken:  "access-token-2",
			TokenType:    "bearer",
			ExpiresIn:    3600,
			RefreshToken: "refresh-token-2",
		})
	})

	mux.HandleFunc("/api/v2/security/defaultTokensExpiry", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, TokenExpiryResponse{
			AccessTokenExpiry:  3600,
			RefreshTokenExpiry: 86400,
		})
	})

	mux.HandleFunc("/api/v2/cluster", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, ClusterInfoResponse{
			Name:    "race-test-cluster",
			Release: "4.2.0",
			Guid:    uuid.New(),
			Capacity: Capacity{
				TotalBytes:         1 << 40,
				UnprovisionedBytes: 1 << 39,
			},
		})
	})

	mux.HandleFunc("/api/v2/users/whoami", func(w http.ResponseWriter, r *http.Request) {
		// Role must satisfy HasCSIPermissions(), or Login() fails permission checks.
		writeJSON(t, w, WhoamiResponse{
			OrgId:    1,
			Username: "admin",
			Source:   "local",
			Uid:      uuid.New(),
			Role:     ApiUserRoleClusterAdmin,
			OrgName:  "Root",
		})
	})

	// Backs GetNfsInterfaceGroup/GetNfsMountIp for TestGetNfsInterfaceGroup_ConcurrentAccess.
	mux.HandleFunc("/api/v2/interfaceGroups", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []InterfaceGroup{
			{Name: "default", Type: InterfaceGroupTypeNFS, Ips: []string{"127.0.0.1"}},
		})
	})

	// Registered under both paths: the mock cluster reports a version recent enough that
	// CompatibilityMap.NewNodeApiObjectPath is true, so WekaNode.GetBasePath resolves to
	// "processes" rather than "nodes".
	nodesHandler := func(w http.ResponseWriter, r *http.Request) {
		gen := atomic.AddInt32(&endpointGen, 1)
		// Both aliases resolve back to the loopback address this httptest server is bound to,
		// so every combination below stays reachable while genuinely varying the endpoint set.
		aliases := []string{"127.0.0.1", "localhost"}
		var ips []string
		switch gen % 3 {
		case 0:
			ips = aliases[:1]
		case 1:
			ips = aliases[1:]
		default:
			ips = aliases
		}
		nodes := make([]WekaNode, 0, len(ips))
		for _, ip := range ips {
			nodes = append(nodes, WekaNode{
				Id:       fmt.Sprintf("Node.%d", gen),
				Mode:     NodeModeBackend,
				Uid:      uuid.New(),
				Hostname: "race-test-node",
				Ips:      []string{ip},
				MgmtPort: mgmtPort,
				Roles:    []string{NodeRoleManagement, NodeRoleBackend},
				Status:   "UP",
			})
		}
		writeJSON(t, w, nodes)
	}
	mux.HandleFunc("/api/v2/nodes", nodesHandler)
	mux.HandleFunc("/api/v2/processes", nodesHandler)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("failed to parse test server URL: %v", err)
	}
	_, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("failed to split test server host/port: %v", err)
	}
	mgmtPort, err = strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("failed to parse test server port: %v", err)
	}
	hostPort = u.Host
	return hostPort, mgmtPort
}

// newRaceTestClient builds an ApiClient pointed at the fake server, with AutoUpdateEndpoints
// enabled so every successful Login() replaces the internal endpoint map.
func newRaceTestClient(t *testing.T, serverAddr string) *ApiClient {
	t.Helper()
	creds := Credentials{
		Username:            "admin",
		Password:            "admin",
		Organization:        "Root",
		HttpScheme:          "http",
		Endpoints:           []string{serverAddr},
		AutoUpdateEndpoints: true,
	}
	c, err := NewApiClient(context.Background(), creds, ApiClientOptions{
		AllowInsecureHttps: true,
		Hostname:           "race-test-client",
	})
	if err != nil {
		t.Fatalf("failed to create API client: %v", err)
	}
	return c
}

// TestApiClientConcurrentUse drives Login, Init, plain requests, and reads of exported state
// concurrently from many goroutines. It is not asserting on API semantics (some errors here are
// expected, e.g. a request landing on a rotated-away endpoint mid-flight) - its job is to give
// `go test -race` enough concurrent access to ApiClient's shared state to flag a real race, and to
// fail if any of that concurrent access panics.
func TestApiClientConcurrentUse(t *testing.T) {
	quietLogs(t)
	serverAddr, _ := newRaceTestServer(t)
	c := newRaceTestClient(t, serverAddr)
	ctx := context.Background()

	const goroutines = 30
	const iterations = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("goroutine %d panicked: %v", id, r)
				}
			}()
			for i := 0; i < iterations; i++ {
				switch i % 4 {
				case 0:
					if err := c.Login(ctx); err != nil {
						t.Logf("goroutine %d: Login: %v", id, err)
					}
				case 1:
					if err := c.Init(ctx); err != nil {
						t.Logf("goroutine %d: Init: %v", id, err)
					}
				case 2:
					resp := &ClusterInfoResponse{}
					if err := c.Get(ctx, ApiPathClusterInfo, nil, resp); err != nil {
						t.Logf("goroutine %d: Get: %v", id, err)
					}
				default:
					_ = c.Hash()
					_ = c.SupportsQuotaDirectoryAsVolume()
					_ = c.SupportsFilesystemAsVolume()
					_ = c.SupportsDirectoryAsVolume()
					_ = c.SupportsMultipleClusters()
					_ = c.GetClusterName()
					_ = c.userRole()
					_ = c.HasCSIPermissions()
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestApiClientConcurrentEndpointRotation focuses specifically on the endpoint bookkeeping:
// UpdateApiEndpoints (which replaces the ApiEndPoints map), rotateEndpoint, and getEndpoint. These
// are unexported, but this test lives in the same package so it can call them directly rather than
// only reaching them indirectly through Login.
func TestApiClientConcurrentEndpointRotation(t *testing.T) {
	quietLogs(t)
	serverAddr, _ := newRaceTestServer(t)
	c := newRaceTestClient(t, serverAddr)
	ctx := context.Background()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("initial login failed: %v", err)
	}

	const goroutines = 30
	const iterations = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("goroutine %d panicked: %v", id, r)
				}
			}()
			for i := 0; i < iterations; i++ {
				switch i % 3 {
				case 0:
					if err := c.UpdateApiEndpoints(ctx); err != nil {
						t.Logf("goroutine %d: UpdateApiEndpoints: %v", id, err)
					}
				case 1:
					c.rotateEndpoint(ctx)
				default:
					if ep := c.getEndpoint(ctx); ep != nil {
						_ = ep.String()
					}
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestGetNfsInterfaceGroup_ConcurrentAccess covers fix 6: nfsInterfaceGroups was a plain map with no
// lock at all, read from GetNfsInterfaceGroup and written from fetchNfsInterfaceGroup. Two goroutines
// reaching it with a cold cache - as two simultaneous NFS publishes on a shared per-cluster client
// would - raced a map write against map reads/writes on other goroutines, which Go turns into an
// unrecoverable "fatal error: concurrent map read and map write" that recover() cannot catch. This
// drives many goroutines through GetNfsMountIp (which resolves the "default" interface group) from a
// cold cache simultaneously; -race must report nothing, and the process must not crash.
func TestGetNfsInterfaceGroup_ConcurrentAccess(t *testing.T) {
	quietLogs(t)
	serverAddr, _ := newRaceTestServer(t)
	c := newRaceTestClient(t, serverAddr)
	ctx := context.Background()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("initial login failed: %v", err)
	}

	const goroutines = 30
	const iterations = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("goroutine %d panicked: %v", id, r)
				}
			}()
			for i := 0; i < iterations; i++ {
				if _, err := c.GetNfsMountIp(ctx); err != nil {
					t.Logf("goroutine %d: GetNfsMountIp: %v", id, err)
				}
			}
		}(g)
	}
	wg.Wait()
}
