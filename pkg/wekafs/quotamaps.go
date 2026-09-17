package wekafs

import (
	"sync"

	"github.com/google/uuid"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// QMLocks hands out one lock per filesystem, so a quota-map refresh for one filesystem does not
// serialise against refreshes of the others.
type QMLocks struct {
	locks sync.Map // map[uuid.UUID]*sync.RWMutex
}

func NewQMLocks() *QMLocks {
	return &QMLocks{}
}

// GetLock returns the lock for uid, creating it on first use.
//
// The create-if-absent must be atomic: with a plain Load-then-Store, two goroutines that both miss
// would each construct a lock and the second would overwrite the first, leaving them holding
// different mutexes for the same filesystem and excluding nothing. LoadOrStore resolves that in one
// step, so every caller for a given uid gets the same lock. The Load first is only a fast path that
// avoids allocating a mutex on the common hit.
func (qml *QMLocks) GetLock(uid uuid.UUID) *sync.RWMutex {
	if lock, ok := qml.locks.Load(uid); ok {
		return lock.(*sync.RWMutex)
	}
	lock, _ := qml.locks.LoadOrStore(uid, &sync.RWMutex{})
	return lock.(*sync.RWMutex)
}

// Note there is deliberately no way to remove a lock. Deleting one is unsound: a goroutine that
// already holds the pointer keeps using it while the next caller creates a fresh mutex for the same
// filesystem, so the two no longer exclude each other. The map only grows with the number of
// filesystems the driver has seen, which is bounded and small.

// QuotaMapsPerFilesystem caches one quota map per filesystem. The maps are replaced wholesale on
// refresh, so readers either see the previous map or the new one, never a partially rebuilt one.
type QuotaMapsPerFilesystem struct {
	mu        sync.RWMutex
	locks     *QMLocks
	quotaMaps map[uuid.UUID]*apiclient.QuotaMap
}

func NewQuotaMapsPerFilesystem() *QuotaMapsPerFilesystem {
	return &QuotaMapsPerFilesystem{
		quotaMaps: make(map[uuid.UUID]*apiclient.QuotaMap),
		locks:     NewQMLocks(),
	}
}

// GetLock returns the refresh lock for a filesystem. Callers take it to keep two refreshes of the
// same filesystem from both hitting the API.
func (qms *QuotaMapsPerFilesystem) GetLock(uid uuid.UUID) *sync.RWMutex {
	return qms.locks.GetLock(uid)
}

// GetQuotaMap returns the cached map for a filesystem, or nil if none has been fetched yet.
func (qms *QuotaMapsPerFilesystem) GetQuotaMap(uid uuid.UUID) *apiclient.QuotaMap {
	qms.mu.RLock()
	defer qms.mu.RUnlock()
	return qms.quotaMaps[uid]
}

// SetQuotaMap installs a freshly fetched map for a filesystem.
func (qms *QuotaMapsPerFilesystem) SetQuotaMap(uid uuid.UUID, quotaMap *apiclient.QuotaMap) {
	qms.mu.Lock()
	defer qms.mu.Unlock()
	qms.quotaMaps[uid] = quotaMap
}

// Forget drops the cached map for a filesystem, e.g. once it is no longer observed. The per
// filesystem lock is intentionally left in place - see the note on QMLocks.
func (qms *QuotaMapsPerFilesystem) Forget(uid uuid.UUID) {
	qms.mu.Lock()
	defer qms.mu.Unlock()
	delete(qms.quotaMaps, uid)
}

// Len reports how many filesystems currently have a cached quota map.
func (qms *QuotaMapsPerFilesystem) Len() int {
	qms.mu.RLock()
	defer qms.mu.RUnlock()
	return len(qms.quotaMaps)
}
