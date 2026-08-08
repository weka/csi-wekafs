package apiclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fsServer(t *testing.T, calls *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "fileSystems") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"access_token": "t", "refresh_token": "r", "expires_in": 3600},
			})
			return
		}
		atomic.AddInt32(calls, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"name": "default"}, {"name": "snapvols"}},
		})
	}))
}

func fsTestClient(t *testing.T, srv *httptest.Server) *ApiClient {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	c, err := NewApiClient(t.Context(), Credentials{
		Username: "u", Password: "p", Organization: "Root",
		Endpoints: []string{u.Host}, HttpScheme: "http",
	}, ApiClientOptions{AllowInsecureHttps: true, Hostname: "test"})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	c.apiToken = "t"
	c.CompatibilityMap = &WekaCompatibilityMap{UrlQueryParams: true}
	return c
}

func TestCachedGetFileSystemByNameServesFromCache(t *testing.T) {
	var calls int32
	srv := fsServer(t, &calls)
	defer srv.Close()
	c := fsTestClient(t, srv)

	fs, err := c.CachedGetFileSystemByName(t.Context(), "default", time.Minute)
	if err != nil || fs == nil || fs.Name != "default" {
		t.Fatalf("first lookup: fs=%v err=%v", fs, err)
	}
	// A second name from the same listing must not cost another request.
	fs2, err := c.CachedGetFileSystemByName(t.Context(), "snapvols", time.Minute)
	if err != nil || fs2 == nil || fs2.Name != "snapvols" {
		t.Fatalf("second lookup: fs=%v err=%v", fs2, err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected one listing to serve both names, got %d requests", got)
	}
}

// Each cached name must keep its own filesystem. Building entries from the address of a loop
// variable used to alias them all onto the last element.
func TestCachedFileSystemsAreDistinct(t *testing.T) {
	var calls int32
	srv := fsServer(t, &calls)
	defer srv.Close()
	c := fsTestClient(t, srv)

	a, _ := c.CachedGetFileSystemByName(t.Context(), "default", time.Minute)
	b, _ := c.CachedGetFileSystemByName(t.Context(), "snapvols", time.Minute)
	if a == nil || b == nil || a.Name == b.Name {
		t.Fatalf("cache entries alias each other: %v / %v", a, b)
	}
}

func TestCachedGetFileSystemByNameHonoursTtl(t *testing.T) {
	var calls int32
	srv := fsServer(t, &calls)
	defer srv.Close()
	c := fsTestClient(t, srv)

	if _, err := c.CachedGetFileSystemByName(t.Context(), "default", time.Minute); err != nil {
		t.Fatalf("warm: %v", err)
	}
	// A zero TTL means nothing cached is acceptable, so this must go back to the API.
	if _, err := c.CachedGetFileSystemByName(t.Context(), "default", 0); err != nil {
		t.Fatalf("ttl 0: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected a zero TTL to force a refetch, got %d requests", got)
	}
}

// The cache is read and written from concurrent request handlers, so its own accessors must be
// safe. This exercises the cache directly rather than through the client, because ApiClient's
// endpoint and token state is not yet concurrency-safe - see the endpoint rewrite still to be
// ported. Run with -race.
func TestFileSystemCacheAccessorsAreConcurrencySafe(t *testing.T) {
	c := &ApiClient{fsCache: make(map[string]*fsCacheEntry)}
	names := []string{"default", "snapvols", "scratch"}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				c.cacheFileSystems([]FileSystem{{Name: names[j%len(names)]}})
				c.cachedFileSystem(names[(j+1)%len(names)], time.Minute)
			}
		}(i)
	}
	wg.Wait()

	for _, n := range names {
		if fs, ok := c.cachedFileSystem(n, time.Minute); !ok || fs.Name != n {
			t.Fatalf("expected %s to be cached under its own name, got %v ok=%v", n, fs, ok)
		}
	}
}
