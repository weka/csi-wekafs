package wekafs

import (
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
