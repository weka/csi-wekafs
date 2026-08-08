package wekafs

import (
	"sync"

	"github.com/google/uuid"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// VolumeMetric is everything the metrics server needs about one PersistentVolume: the Kubernetes
// object, the Weka volume it maps to, the credentials to reach it, and the last statistics read.
type VolumeMetric struct {
	persistentVolume *v1.PersistentVolume
	volume           *Volume
	metrics          *PvStats
	secret           *v1.Secret
	apiClient        *apiclient.ApiClient

	// key is where this volume sits on the cluster, and pvUID identifies it. Both are kept so the
	// volume can be removed from the by-filesystem index without re-deriving them from the Volume,
	// and so removing one of several volumes sharing an inode removes only that one.
	key   volumeKey
	pvUID types.UID
}

// pvName is how an operator identifies this volume: errors and logs from the metrics server name
// the PersistentVolume, not the internal volume id.
func (vm *VolumeMetric) pvName() string {
	if vm == nil || vm.persistentVolume == nil {
		return ""
	}
	return vm.persistentVolume.Name
}

// volumeKey locates a volume on the cluster. Quotas are fetched per filesystem and keyed by inode,
// so this is what a quota map entry has to be matched back to.
type volumeKey struct {
	filesystemUid uuid.UUID
	inodeId       uint64
}

// VolumeMetrics holds the volumes the metrics server is tracking, indexed two ways: by
// PersistentVolume UID, which is how Kubernetes identifies them, and by filesystem and inode, which
// is how the Weka API returns quotas.
//
// The second index maps to a set of volumes rather than to one, because several PersistentVolumes
// can point at the same path - static provisioning makes that entirely ordinary - and they share an
// inode. Keying it to a single volume would let the second silently replace the first, so one of
// them would stop reporting and removing either would unindex both.
//
// One lock guards both indexes. The version this was ported from gave each level of the
// by-filesystem index its own mutex and then acquired them in opposite orders - the filesystem map
// before the inode map when adding, the inode map before the filesystem map when removing - which
// deadlocks if the two run concurrently. Nesting bought nothing: every operation touches both
// levels anyway.
type VolumeMetrics struct {
	mu        sync.RWMutex
	byPv      map[types.UID]*VolumeMetric
	byFsInode map[uuid.UUID]map[uint64]map[types.UID]*VolumeMetric
}

func NewVolumeMetrics() *VolumeMetrics {
	return &VolumeMetrics{
		byPv:      make(map[types.UID]*VolumeMetric),
		byFsInode: make(map[uuid.UUID]map[uint64]map[types.UID]*VolumeMetric),
	}
}

// Add starts tracking a volume. A volume whose filesystem or inode is unknown is tracked by PV only:
// it can still report the capacity Kubernetes knows about, it just cannot be matched to a quota.
func (vms *VolumeMetrics) Add(pvUID types.UID, key volumeKey, vm *VolumeMetric) {
	vm.key = key

	vms.mu.Lock()
	defer vms.mu.Unlock()

	// Replacing an existing entry has to unindex the old one first, in case the volume moved.
	if existing, ok := vms.byPv[pvUID]; ok {
		vms.unindexLocked(existing)
	}
	vm.pvUID = pvUID
	vms.byPv[pvUID] = vm
	if key.filesystemUid == uuid.Nil || key.inodeId == 0 {
		return
	}
	inodes, ok := vms.byFsInode[key.filesystemUid]
	if !ok {
		inodes = make(map[uint64]map[types.UID]*VolumeMetric)
		vms.byFsInode[key.filesystemUid] = inodes
	}
	sharing, ok := inodes[key.inodeId]
	if !ok {
		sharing = make(map[types.UID]*VolumeMetric)
		inodes[key.inodeId] = sharing
	}
	sharing[pvUID] = vm
}

// Remove stops tracking a volume, e.g. once its PersistentVolume is gone.
func (vms *VolumeMetrics) Remove(pvUID types.UID) {
	vms.mu.Lock()
	defer vms.mu.Unlock()
	vm, ok := vms.byPv[pvUID]
	if !ok {
		return
	}
	vms.unindexLocked(vm)
	delete(vms.byPv, pvUID)
}

// unindexLocked drops a volume from the by-filesystem index, and the filesystem's map with it once
// the last volume on that filesystem is gone.
// REQUIRES: vms.mu is held for writing.
func (vms *VolumeMetrics) unindexLocked(vm *VolumeMetric) {
	inodes, ok := vms.byFsInode[vm.key.filesystemUid]
	if !ok {
		return
	}
	sharing, ok := inodes[vm.key.inodeId]
	if !ok {
		return
	}
	// Only this PersistentVolume is removed; any others sharing the inode keep their entry.
	delete(sharing, vm.pvUID)
	if len(sharing) == 0 {
		delete(inodes, vm.key.inodeId)
	}
	if len(inodes) == 0 {
		delete(vms.byFsInode, vm.key.filesystemUid)
	}
}

// Get returns the tracked volume for a PersistentVolume, or nil.
func (vms *VolumeMetrics) Get(pvUID types.UID) *VolumeMetric {
	vms.mu.RLock()
	defer vms.mu.RUnlock()
	return vms.byPv[pvUID]
}

// Has reports whether a PersistentVolume is being tracked.
func (vms *VolumeMetrics) Has(pvUID types.UID) bool {
	vms.mu.RLock()
	defer vms.mu.RUnlock()
	_, ok := vms.byPv[pvUID]
	return ok
}

// ForFilesystem returns the volumes living on one filesystem, so the quotas fetched for it in one
// request can be attributed. The slice is freshly built, so the caller may hold it while the tracked
// set changes underneath.
func (vms *VolumeMetrics) ForFilesystem(fsUid uuid.UUID) []*VolumeMetric {
	vms.mu.RLock()
	defer vms.mu.RUnlock()
	inodes := vms.byFsInode[fsUid]
	out := make([]*VolumeMetric, 0, len(inodes))
	for _, sharing := range inodes {
		for _, vm := range sharing {
			out = append(out, vm)
		}
	}
	return out
}

// ForInode returns every volume at one inode of a filesystem. This is the lookup a quota map entry
// needs, and it returns a slice because one quota can describe several PersistentVolumes pointing at
// the same path - each of them has to be updated from it.
func (vms *VolumeMetrics) ForInode(fsUid uuid.UUID, inodeId uint64) []*VolumeMetric {
	vms.mu.RLock()
	defer vms.mu.RUnlock()
	sharing := vms.byFsInode[fsUid][inodeId]
	out := make([]*VolumeMetric, 0, len(sharing))
	for _, vm := range sharing {
		out = append(out, vm)
	}
	return out
}

// All returns every tracked volume, as a snapshot.
func (vms *VolumeMetrics) All() []*VolumeMetric {
	vms.mu.RLock()
	defer vms.mu.RUnlock()
	out := make([]*VolumeMetric, 0, len(vms.byPv))
	for _, vm := range vms.byPv {
		out = append(out, vm)
	}
	return out
}

// PvUIDs returns the PersistentVolume UIDs currently tracked, as a snapshot, for callers that need
// to work out which of them have gone away.
func (vms *VolumeMetrics) PvUIDs() []types.UID {
	vms.mu.RLock()
	defer vms.mu.RUnlock()
	out := make([]types.UID, 0, len(vms.byPv))
	for uid := range vms.byPv {
		out = append(out, uid)
	}
	return out
}

// Len reports how many volumes are tracked.
func (vms *VolumeMetrics) Len() int {
	vms.mu.RLock()
	defer vms.mu.RUnlock()
	return len(vms.byPv)
}
