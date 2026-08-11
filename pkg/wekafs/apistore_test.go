package wekafs

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// ApiStore's map is read on the fast path of fromCredentials without the lock, while a concurrent
// call writes it under the lock. In Go that is not a benign race but a fatal runtime error
// ("concurrent map read and map write"), and fromCredentials runs on every request that needs an
// API client. Run with -race to make this meaningful.
func TestApiStoreConcurrentAccessIsSafe(t *testing.T) {
	store := &ApiStore{apis: make(map[uint32]*apiclient.ApiClient)}

	var wg sync.WaitGroup
	const writers, readers, iterations = 4, 8, 300

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(base uint32) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				store.Lock()
				store.apis[base*uint32(iterations)+uint32(i)] = &apiclient.ApiClient{}
				store.Unlock()
			}
		}(uint32(w))
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				store.getByHash(uint32(i))
				_, _ = store.getByClusterGuid(uuid.New())
			}
		}()
	}
	wg.Wait()
}

// The write path calls getByHashLocked while already holding the lock; using the locking variant
// there would deadlock, since Go's RWMutex is not reentrant.
func TestApiStoreGetByHashLockedDoesNotRelock(t *testing.T) {
	store := &ApiStore{apis: make(map[uint32]*apiclient.ApiClient)}
	client := &apiclient.ApiClient{}
	store.apis[7] = client

	done := make(chan struct{})
	go func() {
		defer close(done)
		store.Lock()
		defer store.Unlock()
		if got := store.getByHashLocked(7); got != client {
			t.Errorf("expected the stored client back, got %v", got)
		}
		if got := store.getByHashLocked(8); got != nil {
			t.Errorf("expected nil for an absent hash, got %v", got)
		}
	}()
	<-done
}

// validFromSecretsInput is a secret map with every key fromSecrets requires. Each missing-key test
// below starts from a copy of it and deletes exactly the key under test, so a failure can only be
// attributed to that key.
func validFromSecretsInput() map[string]string {
	return map[string]string{
		"endpoints":    "192.168.1.1:14000",
		"username":     "admin",
		"password":     "admin",
		"organization": "Root",
	}
}

// TestFromSecrets_MissingRequiredKey covers the new contract: endpoints, username, password and
// organization are required. Before this, a missing key silently defaulted to an empty string,
// producing a client that could never authenticate and failed obscurely later instead of at
// construction time. fromSecrets returns before touching the network for all of these, so no live
// cluster is needed.
func TestFromSecrets_MissingRequiredKey(t *testing.T) {
	tests := []struct {
		missingKey string
		wantSubstr string
	}{
		{missingKey: "endpoints", wantSubstr: "endpoints"},
		{missingKey: "username", wantSubstr: "username"},
		{missingKey: "password", wantSubstr: "password"},
		{missingKey: "organization", wantSubstr: "organization"},
	}

	store := &ApiStore{apis: make(map[uint32]*apiclient.ApiClient)}
	for _, tt := range tests {
		t.Run(tt.missingKey, func(t *testing.T) {
			secrets := validFromSecretsInput()
			delete(secrets, tt.missingKey)

			client, err := store.fromSecrets(context.Background(), secrets, "test-host")
			if err == nil {
				t.Fatalf("expected an error when %q is missing", tt.missingKey)
			}
			if client != nil {
				t.Fatalf("expected a nil client alongside the error, got %v", client)
			}
			if !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Fatalf("expected error to name the missing key %q, got: %v", tt.wantSubstr, err)
			}
		})
	}
}

// TestFromSecrets_NoValidEndpoints covers the "endpoints" key being present but empty or
// comma-only, which the required-key check alone would not catch since the key exists.
func TestFromSecrets_NoValidEndpoints(t *testing.T) {
	store := &ApiStore{apis: make(map[uint32]*apiclient.ApiClient)}

	tests := map[string]string{
		"empty string": "",
		"commas only":  ",,",
	}
	for name, endpoints := range tests {
		t.Run(name, func(t *testing.T) {
			secrets := validFromSecretsInput()
			secrets["endpoints"] = endpoints

			client, err := store.fromSecrets(context.Background(), secrets, "test-host")
			if err == nil {
				t.Fatalf("expected an error for endpoints value %q", endpoints)
			}
			if client != nil {
				t.Fatalf("expected a nil client alongside the error, got %v", client)
			}
		})
	}
}

// TestFromSecrets_HappyPath documents, rather than exercises, the success path. Once all required
// keys are present and endpoints are non-empty, fromSecrets calls fromCredentials, which calls
// apiclient.NewApiClient (no network) and then newClient.Init(ctx), which performs an actual Login
// HTTP call and retries it with backoff on failure. There is no way to observe a genuine success, or
// even a fast, deterministic failure, without a live cluster reachable at the configured endpoint -
// so this is intentionally not asserted here. Exercising that path is left to the apiclient tests
// that already skip themselves when no cluster is reachable on localhost:14000.
func TestFromSecrets_HappyPath(t *testing.T) {
	t.Skip("fromSecrets' success path only completes after apiclient.ApiClient.Init logs in over " +
		"HTTP; that requires a live Weka cluster and is out of reach for this package's unit tests")
}
