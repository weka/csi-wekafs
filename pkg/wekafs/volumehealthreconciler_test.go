package wekafs

import (
	"testing"
	"time"
)

func TestVolumeConditionCacheStoreAndLookup(t *testing.T) {
	c := newVolumeConditionCache()

	if _, ok := c.lookup("dir/v1/fs/a"); ok {
		t.Fatal("expected a miss on an empty cache")
	}

	c.store("dir/v1/fs/a", volumeConditionEntry{
		known: true, abnormal: true, message: "path is gone", capacity: 1 << 30, probedAt: time.Now(),
	})
	entry, ok := c.lookup("dir/v1/fs/a")
	if !ok {
		t.Fatal("expected a hit for a freshly stored entry")
	}
	if !entry.known || !entry.abnormal || entry.message != "path is gone" || entry.capacity != 1<<30 {
		t.Fatalf("entry did not round-trip: %+v", entry)
	}

	// A probed-but-undeterminable volume is cached as known=false, which callers must report as
	// unknown rather than as healthy.
	c.store("dir/v1/fs/b", volumeConditionEntry{known: false, probedAt: time.Now()})
	if entry, ok := c.lookup("dir/v1/fs/b"); !ok || entry.known {
		t.Fatalf("expected a cached but unknown condition, got ok=%v entry=%+v", ok, entry)
	}
}

// A result older than volumeHealthMaxAge must not be served: reporting a stale condition as current
// is worse than reporting nothing, because the CO cannot tell the difference.
func TestVolumeConditionCacheExpiresStaleEntries(t *testing.T) {
	c := newVolumeConditionCache()
	c.store("dir/v1/fs/a", volumeConditionEntry{
		known: true, message: "healthy", probedAt: time.Now().Add(-volumeHealthMaxAge - time.Minute),
	})
	if _, ok := c.lookup("dir/v1/fs/a"); ok {
		t.Fatal("expected an entry older than volumeHealthMaxAge to be treated as a miss")
	}

	// Just inside the window is still served.
	c.store("dir/v1/fs/b", volumeConditionEntry{
		known: true, message: "healthy", probedAt: time.Now().Add(-volumeHealthMaxAge + time.Minute),
	})
	if _, ok := c.lookup("dir/v1/fs/b"); !ok {
		t.Fatal("expected an entry inside volumeHealthMaxAge to be served")
	}
}

// Without eviction the cache would grow forever as volumes are deleted, and a recreated handle
// could inherit a previous volume's condition.
func TestVolumeConditionCacheRetainOnlyDropsDeletedVolumes(t *testing.T) {
	c := newVolumeConditionCache()
	for _, h := range []string{"a", "b", "c"} {
		c.store(h, volumeConditionEntry{known: true, probedAt: time.Now()})
	}

	remaining, _, _ := c.retainOnly(map[string]struct{}{"a": {}, "c": {}})
	if remaining != 2 {
		t.Fatalf("expected 2 entries to remain, got %d", remaining)
	}
	if _, ok := c.lookup("b"); ok {
		t.Fatal("expected the deleted volume to be evicted")
	}
	for _, h := range []string{"a", "c"} {
		if _, ok := c.lookup(h); !ok {
			t.Fatalf("expected %s to be retained", h)
		}
	}

	if remaining, _, _ := c.retainOnly(map[string]struct{}{}); remaining != 0 {
		t.Fatal("expected an empty live set to clear the cache")
	}
}

// forget is what DeleteVolume uses to remove a volume's cache entry immediately, instead of waiting
// for the next sweep's retainOnly. It must hand back the labels that were cached (so the caller can
// delete the corresponding metric series), and be a harmless no-op for a handle that was never
// cached, or one cached without labels (a probe that never resolved an API client for it).
func TestVolumeConditionCacheForget(t *testing.T) {
	c := newVolumeConditionCache()

	if got := c.forget("never-cached"); got != nil {
		t.Fatalf("expected forgetting an unknown handle to return nil, got %v", got)
	}

	c.store("dir/v1/fs/a", volumeConditionEntry{known: true, probedAt: time.Now(), labels: []string{"csi.weka.io", "pv-a"}})
	c.store("dir/v1/fs/b", volumeConditionEntry{known: false, probedAt: time.Now()}) // never got labels

	if got := c.forget("dir/v1/fs/a"); len(got) != 2 || got[0] != "csi.weka.io" || got[1] != "pv-a" {
		t.Fatalf("expected the stored labels back, got %v", got)
	}
	if _, ok := c.lookup("dir/v1/fs/a"); ok {
		t.Fatal("expected the entry to be gone after forget")
	}

	if got := c.forget("dir/v1/fs/b"); got != nil {
		t.Fatalf("expected forgetting a label-less entry to return nil, got %v", got)
	}
	if _, ok := c.lookup("dir/v1/fs/b"); ok {
		t.Fatal("expected the label-less entry to be gone after forget too")
	}

	// Forgetting twice is a no-op the second time, not an error.
	if got := c.forget("dir/v1/fs/a"); got != nil {
		t.Fatalf("expected a repeat forget to return nil, got %v", got)
	}
}

// The reconciler probes concurrently, so the cache is written from many goroutines while
// ListVolumes reads it. Run with -race to make this meaningful.
func TestVolumeConditionCacheConcurrentAccess(t *testing.T) {
	c := newVolumeConditionCache()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			c.store("dir/v1/fs/a", volumeConditionEntry{known: true, probedAt: time.Now()})
			c.retainOnly(map[string]struct{}{"dir/v1/fs/a": {}})
		}
	}()
	for i := 0; i < 500; i++ {
		c.lookup("dir/v1/fs/a")
	}
	<-done
}
