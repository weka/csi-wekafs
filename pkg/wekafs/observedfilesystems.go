package wekafs

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// ObservedFilesystem is one filesystem the metrics server is watching, and the API client that can
// reach it. It is reference counted by the volumes living on it, so the filesystem stops being
// polled once the last of them goes away.
type ObservedFilesystem struct {
	mu        sync.Mutex
	apiClient *apiclient.ApiClient
	fsUid     uuid.UUID
	fsObj     *apiclient.FileSystem
	// fsObjFetched is when fsObj was last read from the cluster. Staleness is measured from the
	// fetch, not from when a volume last referenced the filesystem: keying it off the reference
	// would mean a filesystem with active volumes never refreshed, because referencing it kept
	// resetting the clock.
	fsObjFetched    time.Time
	lastSeen        time.Time
	lastQuotaUpdate time.Time
	refCount        int
}

// GetApiClient returns the API client for this filesystem.
func (of *ObservedFilesystem) GetApiClient() *apiclient.ApiClient {
	of.mu.Lock()
	defer of.mu.Unlock()
	return of.apiClient
}

// Uid returns the filesystem's uid.
func (of *ObservedFilesystem) Uid() uuid.UUID { return of.fsUid }

// LastQuotaUpdate returns when this filesystem's quotas were last refreshed.
func (of *ObservedFilesystem) LastQuotaUpdate() time.Time {
	of.mu.Lock()
	defer of.mu.Unlock()
	return of.lastQuotaUpdate
}

// MarkQuotaUpdated records that the filesystem's quotas have just been refreshed.
func (of *ObservedFilesystem) MarkQuotaUpdated(at time.Time) {
	of.mu.Lock()
	defer of.mu.Unlock()
	of.lastQuotaUpdate = at
}

// GetFileSystem returns the filesystem object, refetching it when the cached copy is older than
// maxAge. A zero maxAge always refetches.
//
// The lock is deliberately not held across the request: a filesystem lookup can take as long as the
// cluster takes to answer, and holding it would stall every other caller for that whole time. Two
// callers arriving together may both fetch; they converge on the same answer.
func (of *ObservedFilesystem) GetFileSystem(ctx context.Context, maxAge time.Duration) (*apiclient.FileSystem, error) {
	of.mu.Lock()
	cached, fetched, client := of.fsObj, of.fsObjFetched, of.apiClient
	of.mu.Unlock()

	if cached != nil && maxAge > 0 && time.Since(fetched) < maxAge {
		return cached, nil
	}

	// Fetch into a fresh object rather than the cached one: GetFileSystemByUid unmarshals into the
	// value it is given, so passing a nil cached pointer would fail every time and the cache could
	// never recover once it had been cleared.
	fresh := &apiclient.FileSystem{}
	if err := client.GetFileSystemByUid(ctx, of.fsUid, fresh, false); err != nil {
		return nil, err
	}

	of.mu.Lock()
	of.fsObj = fresh
	of.fsObjFetched = time.Now()
	of.mu.Unlock()
	return fresh, nil
}

// ObservedFilesystems is the set of filesystems currently backing at least one observed volume.
type ObservedFilesystems struct {
	mu   sync.RWMutex
	uids map[uuid.UUID]*ObservedFilesystem
}

func NewObservedFilesystems() *ObservedFilesystems {
	return &ObservedFilesystems{uids: make(map[uuid.UUID]*ObservedFilesystem)}
}

// IncRef records that one more volume lives on this filesystem, adding it to the observed set if it
// is the first.
//
// The lookup and the insert happen under one lock. Splitting them - as the version this was ported
// from did, reading under a read lock and then inserting under a write lock - lets two goroutines
// both miss and both insert, so the second overwrites the first and resets a reference count that
// another volume was already counted in. The filesystem then stops being observed while volumes are
// still using it.
func (ofs *ObservedFilesystems) IncRef(fs *apiclient.FileSystem, apiClient *apiclient.ApiClient) {
	if fs == nil || fs.Uid == uuid.Nil {
		return
	}
	now := time.Now()

	ofs.mu.Lock()
	defer ofs.mu.Unlock()
	if existing, ok := ofs.uids[fs.Uid]; ok {
		existing.mu.Lock()
		existing.refCount++
		existing.lastSeen = now
		existing.mu.Unlock()
		return
	}
	ofs.uids[fs.Uid] = &ObservedFilesystem{
		apiClient:    apiClient,
		fsUid:        fs.Uid,
		fsObj:        fs,
		fsObjFetched: now,
		lastSeen:     now,
		refCount:     1,
	}
}

// DecRef records that one fewer volume lives on this filesystem, and reports whether that was the
// last one - in which case the filesystem has been dropped and the caller should discard anything
// else it was keeping for it, such as its cached quota map.
func (ofs *ObservedFilesystems) DecRef(fsUid uuid.UUID) (evicted bool) {
	if fsUid == uuid.Nil {
		return false
	}
	ofs.mu.Lock()
	defer ofs.mu.Unlock()
	of, ok := ofs.uids[fsUid]
	if !ok {
		return false
	}
	of.mu.Lock()
	of.refCount--
	remaining := of.refCount
	of.mu.Unlock()

	if remaining > 0 {
		return false
	}
	delete(ofs.uids, fsUid)
	return true
}

// Get returns one observed filesystem, or nil.
func (ofs *ObservedFilesystems) Get(fsUid uuid.UUID) *ObservedFilesystem {
	ofs.mu.RLock()
	defer ofs.mu.RUnlock()
	return ofs.uids[fsUid]
}

// GetApiClient returns the API client that can reach a filesystem, or nil if it is not observed.
func (ofs *ObservedFilesystems) GetApiClient(fsUid uuid.UUID) *apiclient.ApiClient {
	of := ofs.Get(fsUid)
	if of == nil {
		return nil
	}
	return of.GetApiClient()
}

// All returns the observed filesystems as a snapshot. It deliberately does not hand back the map:
// the caller would be iterating it after the lock was released, while another goroutine inserts.
func (ofs *ObservedFilesystems) All() []*ObservedFilesystem {
	ofs.mu.RLock()
	defer ofs.mu.RUnlock()
	out := make([]*ObservedFilesystem, 0, len(ofs.uids))
	for _, of := range ofs.uids {
		out = append(out, of)
	}
	return out
}

// ByQuotaUpdateTime returns the observed filesystems least recently refreshed first, so a sweep that
// cannot get through all of them starts with the ones most in need.
func (ofs *ObservedFilesystems) ByQuotaUpdateTime() []*ObservedFilesystem {
	out := ofs.All()
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastQuotaUpdate().Before(out[j].LastQuotaUpdate())
	})
	return out
}

// Len reports how many filesystems are being observed.
func (ofs *ObservedFilesystems) Len() int {
	ofs.mu.RLock()
	defer ofs.mu.RUnlock()
	return len(ofs.uids)
}
