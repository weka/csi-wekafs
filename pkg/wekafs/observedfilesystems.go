package wekafs

import (
	"context"
	"github.com/google/uuid"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"go.uber.org/atomic"
	"sync"
	"time"
)

type ObservedFilesystems struct {
	sync.RWMutex
	uids map[uuid.UUID]*ObservedFilesystem // map[filesystemUUID]int, where int is the number of references to this filesystem
	ms   *MetricsServer
}

func (ofu *ObservedFilesystems) GetMap() map[uuid.UUID]*ObservedFilesystem {
	ofu.RLock()
	defer ofu.RUnlock()
	return ofu.uids // return a copy of the map
}

func (ofu *ObservedFilesystems) GetByUid(uid uuid.UUID) *ObservedFilesystem {
	ofu.RLock()
	defer ofu.RUnlock()
	if existing, exists := ofu.uids[uid]; exists {
		return existing // return the ObservedFilesystem for the given UID
	}
	return nil
}

func (ofu *ObservedFilesystems) incRef(fs *apiclient.FileSystem, apiClient *apiclient.ApiClient) {
	if fs == nil || fs.Uid == uuid.Nil {
		return // nothing to do
	}
	of := ofu.GetByUid(fs.Uid)
	if of != nil {
		of.incRef()
	} else {
		ofu.Lock()
		defer ofu.Unlock()
		ofu.uids[fs.Uid] = &ObservedFilesystem{
			apiClient:  apiClient,
			refCounter: atomic.NewInt32(1), // start with a reference count of 1
			fsObj:      fs,
			fsUid:      fs.Uid,
			lastSeen:   atomic.NewTime(time.Now()),
		}
	}
}

func (ofu *ObservedFilesystems) decRef(fs *apiclient.FileSystem) {
	if fs == nil || fs.Uid == uuid.Nil {
		return // nothing to do
	}
	ofu.Lock()
	defer ofu.Unlock()
	of, exists := ofu.uids[fs.Uid]
	if exists {
		of.Lock()
		defer of.Unlock()
		of.refCounter.Dec()
		if of.refCounter.Load() <= 0 {
			// remove the filesystem from the map if no references are left
			delete(ofu.uids, fs.Uid)
			ofu.ms.quotaMaps.DeleteLock(fs.Uid) // to avoid memory leaks, delete the lock after the last reference is removed
		}
	}
}

func (ofu *ObservedFilesystems) GetApiClient(uid uuid.UUID) *apiclient.ApiClient {
	existing := ofu.GetByUid(uid)
	if existing == nil {
		return nil
	}
	return existing.apiClient // return the API client for the filesystem
}

func NewObservedFilesystems(ms *MetricsServer) *ObservedFilesystems {
	return &ObservedFilesystems{
		uids: make(map[uuid.UUID]*ObservedFilesystem),
		ms:   ms,
	}
}

type ObservedFilesystem struct {
	sync.Mutex
	apiClient  *apiclient.ApiClient // the API client for this filesystem
	fsUid      uuid.UUID
	fsObj      *apiclient.FileSystem
	lastSeen   *atomic.Time
	refCounter *atomic.Int32
}

func (ofu *ObservedFilesystem) incRef() {
	of := ofu
	of.Lock()
	defer of.Unlock()
	of.refCounter.Inc()
	of.lastSeen.Store(time.Now())
}

func (ofu *ObservedFilesystem) decRef() {
	of := ofu
	of.Lock()
	defer of.Unlock()
	of.refCounter.Dec()
}

func (ofu *ObservedFilesystem) GetApiClient() *apiclient.ApiClient {
	return ofu.apiClient // return the API client for this filesystem
}

func (ofu *ObservedFilesystem) GetFileSystem(ctx context.Context, fromCache bool) *apiclient.FileSystem {
	ofu.Lock()
	defer ofu.Unlock()
	if ofu.fsObj == nil || !fromCache || ofu.lastSeen.Load().Add(1*time.Minute).Before(time.Now()) {
		err := ofu.apiClient.GetFileSystemByUid(ctx, ofu.fsUid, ofu.fsObj, false)
		if err != nil {
			ofu.fsObj = nil // reset the filesystem object if there was an error
			return nil      // return nil if the filesystem could not be fetched
		}
	}
	return ofu.fsObj // return the filesystem object
}
