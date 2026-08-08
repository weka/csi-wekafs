package apiclient

import (
	"context"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient/apiclienttest"
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
// It stands up a fake Weka REST API via apiclienttest and never touches a real cluster or
// localhost:14000.

// newRaceTestClient builds an ApiClient pointed at the fake server, with AutoUpdateEndpoints
// enabled so every successful Login() replaces the internal endpoint map.
func newRaceTestClient(t *testing.T, server *apiclienttest.Server) *ApiClient {
	t.Helper()
	creds := Credentials{
		Username:            "admin",
		Password:            "admin",
		Organization:        "Root",
		HttpScheme:          "http",
		Endpoints:           []string{server.Addr()},
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
	server := apiclienttest.New(t, apiclienttest.WithRotatingEndpoints())
	c := newRaceTestClient(t, server)
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
					_ = c.ClusterName
					_ = c.ApiUserRole
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
	server := apiclienttest.New(t, apiclienttest.WithRotatingEndpoints())
	c := newRaceTestClient(t, server)
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
	server := apiclienttest.New(t, apiclienttest.WithRotatingEndpoints())
	c := newRaceTestClient(t, server)
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
