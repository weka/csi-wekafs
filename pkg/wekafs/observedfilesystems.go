package wekafs

import (
	"github.com/google/uuid"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"sync"
)

type ObservedFilesystemUids struct {
	sync.Mutex
	uids map[uuid.UUID]*ObservedFilesystemUid // map[filesystemUUID]int, where int is the number of references to this filesystem
}

func (ofu *ObservedFilesystemUids) incRef(fs *apiclient.FileSystem, apiClient *apiclient.ApiClient) {
	if fs == nil || fs.Uid == uuid.Nil {
		return // nothing to do
	}
	ofu.Lock()
	defer ofu.Unlock()
	if ofu.uids == nil {
		ofu.uids = make(map[uuid.UUID]*ObservedFilesystemUid)
	}
	if existing, exists := ofu.uids[fs.Uid]; exists {
		existing.refCounter++
		ofu.uids[fs.Uid] = existing
	} else {
		ofu.uids[fs.Uid] = &ObservedFilesystemUid{
			apiClient:  apiClient,
			refCounter: 1,
		}
	}
}

func (ofu *ObservedFilesystemUids) decRef(fs *apiclient.FileSystem) {
	if fs == nil || fs.Uid == uuid.Nil {
		return // nothing to do
	}
	ofu.Lock()
	defer ofu.Unlock()
	if ofu.uids == nil {
		return // nothing to do
	}
	if existing, exists := ofu.uids[fs.Uid]; exists {
		existing.refCounter--
		if existing.refCounter <= 0 {
			delete(ofu.uids, fs.Uid) // remove if no references left
		} else {
			ofu.uids[fs.Uid] = existing // update the reference count
		}
	}
}

func (ofu *ObservedFilesystemUids) GetApiClient(uid uuid.UUID) *apiclient.ApiClient {
	ofu.Lock()
	defer ofu.Unlock()
	if ofu.uids == nil {
		return nil // no observed filesystems
	}
	if existing, exists := ofu.uids[uid]; exists {
		return existing.apiClient // return the API client for the filesystem
	}
	return nil // filesystem not found
}

// SetApiClient sets the API client for a filesystem
func (ofu *ObservedFilesystemUids) SetApiClient(uid uuid.UUID, apiClient *apiclient.ApiClient) {
	ofu.Lock()
	defer ofu.Unlock()
	if ofu.uids == nil {
		ofu.uids = make(map[uuid.UUID]*ObservedFilesystemUid)
	}
	if existing, exists := ofu.uids[uid]; exists {
		existing.apiClient = apiClient // update the API client
	}
}

func NewObservedFilesystemUids() *ObservedFilesystemUids {
	return &ObservedFilesystemUids{}
}

type ObservedFilesystemUid struct {
	apiClient  *apiclient.ApiClient // the API client for this filesystem
	refCounter int
}
