package wekafs

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

func TestObservedFilesystemsRefCounting(t *testing.T) {
	ofs := NewObservedFilesystems()
	fs := &apiclient.FileSystem{Uid: uuid.New(), Name: "default"}

	ofs.IncRef(fs, nil)
	ofs.IncRef(fs, nil)
	if ofs.Len() != 1 {
		t.Fatalf("Len() = %d, want 1 - two volumes on one filesystem is still one filesystem", ofs.Len())
	}

	if evicted := ofs.DecRef(fs.Uid); evicted {
		t.Error("filesystem was evicted while a volume still referenced it")
	}
	if ofs.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", ofs.Len())
	}

	if evicted := ofs.DecRef(fs.Uid); !evicted {
		t.Error("DecRef did not report eviction when the last reference went away")
	}
	if ofs.Len() != 0 {
		t.Errorf("Len() = %d, want 0", ofs.Len())
	}
	if got := ofs.Get(fs.Uid); got != nil {
		t.Errorf("Get returned %v after eviction", got)
	}
	// Decrementing an unobserved filesystem is a no-op, not a panic or a negative count.
	if evicted := ofs.DecRef(fs.Uid); evicted {
		t.Error("DecRef on an unknown filesystem reported an eviction")
	}
	if evicted := ofs.DecRef(uuid.Nil); evicted {
		t.Error("DecRef on the nil uid reported an eviction")
	}
}

func TestObservedFilesystemsIgnoresNil(t *testing.T) {
	ofs := NewObservedFilesystems()
	ofs.IncRef(nil, nil)
	ofs.IncRef(&apiclient.FileSystem{}, nil) // uid is nil
	if ofs.Len() != 0 {
		t.Errorf("Len() = %d, want 0 - a filesystem with no uid must not be observed", ofs.Len())
	}
}

// The reference count must survive concurrent adds and removes. The version this was ported from
// looked the filesystem up under a read lock and inserted under a write lock, so two goroutines
// could both miss and both insert - the second overwriting a count the first had already
// contributed to, which drops a filesystem that still has volumes on it.
func TestObservedFilesystemsConcurrentRefCounting(t *testing.T) {
	ofs := NewObservedFilesystems()
	fs := &apiclient.FileSystem{Uid: uuid.New(), Name: "default"}

	const refs = 200
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < refs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release together, so the miss-then-insert windows overlap
			ofs.IncRef(fs, nil)
		}()
	}
	close(start)
	wg.Wait()

	if ofs.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", ofs.Len())
	}

	// Exactly one of the DecRefs - the last - may report eviction. If IncRef lost increments, an
	// earlier DecRef evicts while references remain.
	evictions := 0
	for i := 0; i < refs; i++ {
		if ofs.DecRef(fs.Uid) {
			evictions++
			if i != refs-1 {
				t.Fatalf("filesystem evicted after %d of %d DecRefs - IncRef lost increments", i+1, refs)
			}
		}
	}
	if evictions != 1 {
		t.Errorf("got %d evictions, want exactly 1", evictions)
	}
}

func TestObservedFilesystemsByQuotaUpdateTime(t *testing.T) {
	ofs := NewObservedFilesystems()
	oldest := &apiclient.FileSystem{Uid: uuid.New(), Name: "oldest"}
	middle := &apiclient.FileSystem{Uid: uuid.New(), Name: "middle"}
	newest := &apiclient.FileSystem{Uid: uuid.New(), Name: "newest"}
	for _, fs := range []*apiclient.FileSystem{oldest, middle, newest} {
		ofs.IncRef(fs, nil)
	}

	now := time.Now()
	ofs.Get(oldest.Uid).MarkQuotaUpdated(now.Add(-time.Hour))
	ofs.Get(middle.Uid).MarkQuotaUpdated(now.Add(-time.Minute))
	ofs.Get(newest.Uid).MarkQuotaUpdated(now)

	got := ofs.ByQuotaUpdateTime()
	if len(got) != 3 {
		t.Fatalf("got %d filesystems, want 3", len(got))
	}
	// Least recently refreshed first, so a sweep that runs out of time does the neediest ones.
	want := []uuid.UUID{oldest.Uid, middle.Uid, newest.Uid}
	for i, uid := range want {
		if got[i].Uid() != uid {
			t.Errorf("position %d is %v, want %v - order must be least recently updated first",
				i, got[i].Uid(), uid)
		}
	}
}

// All must hand back a snapshot: the caller iterates it after the lock is released.
func TestObservedFilesystemsAllIsASnapshot(t *testing.T) {
	ofs := NewObservedFilesystems()
	first := &apiclient.FileSystem{Uid: uuid.New()}
	ofs.IncRef(first, nil)

	snapshot := ofs.All()
	ofs.IncRef(&apiclient.FileSystem{Uid: uuid.New()}, nil)
	ofs.DecRef(first.Uid)

	if len(snapshot) != 1 || snapshot[0].Uid() != first.Uid {
		t.Errorf("the snapshot changed when the observed set did: %v", snapshot)
	}
}

func TestObservedFilesystemsConcurrentAccess(t *testing.T) {
	ofs := NewObservedFilesystems()
	filesystems := make([]*apiclient.FileSystem, 6)
	for i := range filesystems {
		filesystems[i] = &apiclient.FileSystem{Uid: uuid.New()}
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				fs := filesystems[j%len(filesystems)]
				ofs.IncRef(fs, nil)
				_ = ofs.All()
				_ = ofs.ByQuotaUpdateTime()
				_ = ofs.GetApiClient(fs.Uid)
				if of := ofs.Get(fs.Uid); of != nil {
					of.MarkQuotaUpdated(time.Now())
					_ = of.LastQuotaUpdate()
				}
				ofs.DecRef(fs.Uid)
			}
		}(i)
	}
	wg.Wait()

	if ofs.Len() != 0 {
		t.Errorf("Len() = %d, want 0 - every IncRef was matched by a DecRef", ofs.Len())
	}
}
