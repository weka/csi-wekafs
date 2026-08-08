package wekafs

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// TestQMLocksGetLockIsStable is the regression test for the create-if-absent race: concurrent
// callers for the same filesystem must all receive the identical *sync.RWMutex. A Load-then-Store
// implementation hands out more than one and fails here (usually, since it depends on the
// interleaving - run with -race and -count to shake it out).
func TestQMLocksGetLockIsStable(t *testing.T) {
	const goroutines = 50
	locks := NewQMLocks()
	uid := uuid.New()

	got := make([]*sync.RWMutex, goroutines)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release them together, to make the miss-then-create window overlap
			got[i] = locks.GetLock(uid)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, lock := range got {
		if lock == nil {
			t.Fatalf("goroutine %d got a nil lock", i)
		}
		if lock != got[0] {
			t.Fatalf("goroutine %d got a different lock (%p) than goroutine 0 (%p): "+
				"callers would not exclude each other", i, lock, got[0])
		}
	}
}

// A lock actually excludes, and different filesystems do not share one.
func TestQMLocksPerFilesystem(t *testing.T) {
	locks := NewQMLocks()
	a, b := uuid.New(), uuid.New()

	if locks.GetLock(a) == locks.GetLock(b) {
		t.Fatal("two filesystems share a lock, so their refreshes would serialise")
	}
	if locks.GetLock(a) != locks.GetLock(a) {
		t.Fatal("repeated GetLock for one filesystem returned different locks")
	}

	lock := locks.GetLock(a)
	counter := 0
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lock.Lock()
			defer lock.Unlock()
			counter++ // -race proves the lock is doing its job
		}()
	}
	wg.Wait()
	if counter != 100 {
		t.Fatalf("counter = %d, want 100", counter)
	}
}

func TestQuotaMapsPerFilesystem(t *testing.T) {
	qms := NewQuotaMapsPerFilesystem()
	uid := uuid.New()

	if got := qms.GetQuotaMap(uid); got != nil {
		t.Fatalf("expected no cached map before one is set, got %v", got)
	}
	if got := qms.Len(); got != 0 {
		t.Fatalf("Len() = %d, want 0", got)
	}

	quotaMap := &apiclient.QuotaMap{
		FileSystemUid: uid,
		Quotas:        map[uint64]*apiclient.Quota{42: {InodeId: 42, TotalBytes: 1024}},
	}
	qms.SetQuotaMap(uid, quotaMap)

	got := qms.GetQuotaMap(uid)
	if got != quotaMap {
		t.Fatalf("GetQuotaMap returned %v, want the map that was set", got)
	}
	if q := got.GetQuotaForInodeId(42); q == nil || q.TotalBytes != 1024 {
		t.Fatalf("GetQuotaForInodeId(42) = %v, want the quota with 1024 bytes", q)
	}
	if q := got.GetQuotaForInodeId(43); q != nil {
		t.Fatalf("GetQuotaForInodeId(43) = %v, want nil for an inode with no quota", q)
	}
	if got := qms.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1", got)
	}

	qms.Forget(uid)
	if got := qms.GetQuotaMap(uid); got != nil {
		t.Fatalf("expected the map to be dropped, got %v", got)
	}
}

// Concurrent readers and writers across filesystems must not race.
func TestQuotaMapsPerFilesystemConcurrent(t *testing.T) {
	qms := NewQuotaMapsPerFilesystem()
	uids := make([]uuid.UUID, 8)
	for i := range uids {
		uids[i] = uuid.New()
	}

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			uid := uids[i%len(uids)]
			lock := qms.GetLock(uid)
			lock.Lock()
			qms.SetQuotaMap(uid, &apiclient.QuotaMap{FileSystemUid: uid, Quotas: map[uint64]*apiclient.Quota{}})
			lock.Unlock()
			_ = qms.GetQuotaMap(uid)
			_ = qms.Len()
		}(i)
	}
	wg.Wait()

	if got := qms.Len(); got != len(uids) {
		t.Fatalf("Len() = %d, want %d", got, len(uids))
	}
}
