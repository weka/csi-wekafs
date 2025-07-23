package wekafs

import (
	"context"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sync"
)

// VolumeMetric represents the prometheusMetrics for a single Persistent Volume in Kubernetes
type VolumeMetric struct {
	persistentVolume *v1.PersistentVolume // object that represents the Kubernetes Persistent Volume
	volume           *Volume              // object that represents the Weka CSI Volume
	metrics          *PvStats             // Weka metrics for the volume including capacity, used, free, reads, writes, readBytes, writeBytes, writeThroughput
	secret           *v1.Secret           // Kubernetes Secret associated with the volume
	apiClient        *apiclient.ApiClient // reference to the Weka API client
}

type PerInodeIndex struct {
	sync.RWMutex
	m map[uint64]*VolumeMetric
}

func NewPerInodeIndex() *PerInodeIndex {
	return &PerInodeIndex{
		m: make(map[uint64]*VolumeMetric),
	}
}

func (pi *PerInodeIndex) Get(inodeId uint64) *VolumeMetric {
	pi.RLock()
	defer pi.RUnlock()
	if metric, exists := pi.m[inodeId]; exists {
		return metric
	}
	return nil
}

type PerFsInodeIndex struct {
	sync.RWMutex
	m map[uuid.UUID]*PerInodeIndex // map of Filesystem UID to PerInodeIndex
}

type VmIndex struct {
	sync.RWMutex
	index *PerFsInodeIndex
}

func (vmi *VmIndex) GetForFilesystem(fsUid uuid.UUID) *PerInodeIndex {
	if fsMetrics, exists := vmi.index.m[fsUid]; exists {
		return fsMetrics
	}
	return nil
}

func (vmi *VmIndex) GetVolumeMetric(fsUid uuid.UUID, inodeId uint64) *VolumeMetric {
	fsMetrics := vmi.GetForFilesystem(fsUid)

	if fsMetrics == nil {
		return nil // no metrics for this filesystem
	}
	fsMetrics.RLock()
	defer fsMetrics.RUnlock()
	if volumeMetric, exists := fsMetrics.m[inodeId]; exists {
		return volumeMetric
	}
	return nil
}

func (vmi *VmIndex) Add(fsUid uuid.UUID, inodeId uint64, metric *VolumeMetric) {
	vmi.RLock()
	pi, exists := vmi.index.m[fsUid]
	vmi.RUnlock()
	if !exists {
		pi = NewPerInodeIndex()
		vmi.Lock()
		vmi.index.m[fsUid] = pi
		vmi.Unlock()
	}
	pi.Lock()
	defer pi.Unlock()
	pi.m[inodeId] = metric
}

// Remove removes a VolumeMetric from the index based on filesystem UID and inode ID
func (vmi *VmIndex) Remove(fsUid uuid.UUID, inodeId uint64) {
	pi := vmi.GetForFilesystem(fsUid)
	if pi == nil {
		return // nothing to remove
	}

	pi.Lock()
	defer pi.Unlock()
	if _, exists := pi.m[inodeId]; exists {
		delete(pi.m, inodeId)
		if len(pi.m) == 0 { // if no metrics left for this filesystem
			vmi.Lock()
			defer vmi.Unlock()
			delete(vmi.index.m, fsUid) // remove the filesystem entry
		}
	}
}

func NewVmFilesystemIndex() *VmIndex {
	return &VmIndex{
		index: &PerFsInodeIndex{
			m: make(map[uuid.UUID]*PerInodeIndex),
		},
	}
}

type VolumeMetrics struct {
	sync.RWMutex
	Metrics map[types.UID]*VolumeMetric
	index   *VmIndex
}

func (vms *VolumeMetrics) HasVolumeMetric(pvUID types.UID) bool {
	if vms.Metrics == nil {
		return false
	}
	vms.RLock()
	defer vms.RUnlock()
	_, exists := vms.Metrics[pvUID]
	return exists
}

func (vms *VolumeMetrics) GetVolumeMetric(pvUID types.UID) *VolumeMetric {
	vms.RLock()
	defer vms.RUnlock()
	if _, exists := vms.Metrics[pvUID]; exists {
		return vms.Metrics[pvUID]
	}
	return nil
}

func (vms *VolumeMetrics) GetAllMetricsByFilesystemUid(ctx context.Context, fsUid uuid.UUID) []*VolumeMetric {
	var metrics []*VolumeMetric
	index := vms.index.GetForFilesystem(fsUid)
	if index == nil {
		log.Ctx(ctx).Debug().Msgf("No volume metrics found for filesystem %s", fsUid.String())
		return metrics // return empty slice if no metrics found
	}
	for _, vm := range index.m {
		if vm != nil {
			metrics = append(metrics, vm)
		}
	}
	return metrics

}

func (vms *VolumeMetrics) AddVolumeMetric(ctx context.Context, pvUID types.UID, vm *VolumeMetric) {
	if vm.volume != nil { // if the volume is not nil, we can add it to the index. should never happen, but just in case
		if vm.volume.fileSystemObject == nil {
			panic("VolumeMetric has no filesystem object, cannot add to index")
		}
		inodeId := vm.volume.inodeId
		if inodeId == 0 {
			panic("Volume metric has no inode ID, cannot add to index")
		}
		vms.index.Add(vm.volume.fileSystemObject.Uid, inodeId, vm)
	}
	vms.Lock()
	defer vms.Unlock()
	vms.Metrics[pvUID] = vm
}

func (vms *VolumeMetrics) RemoveVolumeMetric(ctx context.Context, pvUID types.UID) {
	logger := log.Ctx(ctx)
	vms.RLock()
	vm, exists := vms.Metrics[pvUID]
	vms.RUnlock()
	if !exists {
		return // nothing to remove
	}
	fsObj := vm.volume.fileSystemObject
	if fsObj == nil {
		logger.Error().Msg("Failed to get filesystem object for volume metric")
		return
	}
	inodeId, err := vm.volume.getInodeId(ctx)
	if err != nil || inodeId == 0 {
		logger.Error().Msg("Volume metric has no inode ID, cannot add to index")
		return
	}
	vms.index.Remove(fsObj.Uid, inodeId)
	vms.Lock()
	defer vms.Unlock()
	delete(vms.Metrics, pvUID)

}

func NewVolumeMetrics() *VolumeMetrics {
	return &VolumeMetrics{
		Metrics: make(map[types.UID]*VolumeMetric),
		index:   NewVmFilesystemIndex(),
	}
}
