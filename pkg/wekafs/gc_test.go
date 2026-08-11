package wekafs

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

func testApiClient(t *testing.T, org string) *apiclient.ApiClient {
	t.Helper()
	client, err := apiclient.NewApiClient(context.Background(), apiclient.Credentials{
		Username:     "csi",
		Password:     "secret",
		Organization: org,
		Endpoints:    []string{"10.0.0.1:14000"},
		HttpScheme:   "https",
	}, apiclient.ApiClientOptions{AllowInsecureHttps: true, Hostname: "test-host"})
	if err != nil {
		t.Fatalf("could not build api client for %s: %v", org, err)
	}
	return client
}

// Two tenants may each own a filesystem called "default". Keying GC state by name alone made one
// tenant's purge suppress the other's, stranding the second tenant's trash indefinitely.
func TestGcKeySeparatesTenantsSharingAFilesystemName(t *testing.T) {
	tenantA := testApiClient(t, "tenant-a")
	tenantB := testApiClient(t, "tenant-b")
	if tenantA.Hash() == tenantB.Hash() {
		t.Fatal("expected different tenants to hash differently")
	}

	keyA := newGcKey("default", tenantA)
	keyB := newGcKey("default", tenantB)
	if keyA == keyB {
		t.Fatal("same filesystem name on different tenants must not share GC state")
	}

	// The same filesystem on the same tenant must resolve to one key, or a claim taken by
	// initiateGarbageCollection would never be released by purgeLeftovers and GC would wedge.
	if newGcKey("default", tenantA) != keyA {
		t.Fatal("key must be stable for the same filesystem and client")
	}
	if newGcKey("other", tenantA) == keyA {
		t.Fatal("different filesystems on one tenant must not share GC state")
	}

	// Simulate the claim/release cycle across both tenants through the real maps.
	gc := initInnerPathVolumeGc(nil)
	gc.isRunning[keyA] = true
	if gc.isRunning[keyB] {
		t.Fatal("a purge running for tenant A must leave tenant B free to start")
	}
}

// Legacy, API-unbound volumes have no client; they must still get a usable, stable key.
func TestGcKeyWithoutApiClient(t *testing.T) {
	key := newGcKey("default", nil)
	if key.apiClientHash != 0 || key.filesystem != "default" {
		t.Fatalf("unexpected key for API-unbound volume: %+v", key)
	}
	if newGcKey("default", nil) != key {
		t.Fatal("key for API-unbound volumes must be stable")
	}
	if newGcKey("default", testApiClient(t, "tenant-a")) == key {
		t.Fatal("API-bound and API-unbound volumes must not share GC state")
	}
}

// TestRenameIntoTrash_ConcurrentAttemptsOnTheSameVolume covers CSI-380. The check-then-rename in
// renameIntoTrash is not atomic on its own: two attempts on the same volume - which is what a
// retried DeleteVolume produces - could both see the source present, and whichever lost the race
// would then fail renaming a path the other had already moved, leaving the volume stuck in
// Deleting. Every attempt must report success, and the contents must land in the trash exactly once.
func TestRenameIntoTrash_ConcurrentAttemptsOnTheSameVolume(t *testing.T) {
	const attempts = 16

	root := t.TempDir()
	trash := filepath.Join(root, garbagePath)
	volume := filepath.Join(root, "csi-volumes", "pvc-stuck-in-deleting")
	if err := os.MkdirAll(volume, DefaultVolumePermissions); err != nil {
		t.Fatalf("failed to seed the volume directory: %v", err)
	}
	marker := filepath.Join(volume, "payload")
	if err := os.WriteFile(marker, []byte("data"), 0600); err != nil {
		t.Fatalf("failed to seed volume contents: %v", err)
	}

	gc := &innerPathVolGc{}
	errs := make(chan error, attempts)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- gc.renameIntoTrash(context.Background(), volume, trash)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent attempt failed instead of treating the move as already done: %v", err)
		}
	}

	if _, err := os.Stat(volume); !os.IsNotExist(err) {
		t.Fatalf("expected the volume directory to be gone from its original location, stat returned %v", err)
	}
	moved, err := os.ReadDir(trash)
	if err != nil {
		t.Fatalf("failed to read the trash directory: %v", err)
	}
	if len(moved) != 1 || moved[0].Name() != "pvc-stuck-in-deleting" {
		t.Fatalf("expected exactly one directory in the trash, got %d entries", len(moved))
	}
	if _, err := os.Stat(filepath.Join(trash, "pvc-stuck-in-deleting", "payload")); err != nil {
		t.Fatalf("expected the volume contents to survive the move: %v", err)
	}
}
