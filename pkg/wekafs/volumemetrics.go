package wekafs

import (
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

type VolumeMetrics struct {
	sync.Mutex
	Metrics map[types.UID]*VolumeMetric
}

func (vms *VolumeMetrics) HasVolumeMetric(pvUID types.UID) bool {
	vms.Lock()
	defer vms.Unlock()
	_, exists := vms.Metrics[pvUID]
	return exists
}

func (vms *VolumeMetrics) GetVolumeMetric(pvUID types.UID) *VolumeMetric {
	vms.Lock()
	defer vms.Unlock()
	if _, exists := vms.Metrics[pvUID]; exists {
		return vms.Metrics[pvUID]
	}
	return nil
}

func (vms *VolumeMetrics) AddVolumeMetric(pvUID types.UID, metric *VolumeMetric) {
	vms.Lock()
	defer vms.Unlock()
	if vms.Metrics == nil {
		vms.Metrics = make(map[types.UID]*VolumeMetric)
	}
	vms.Metrics[pvUID] = metric
}

func (vms *VolumeMetrics) RemoveVolumeMetric(pvUID types.UID) {
	vms.Lock()
	defer vms.Unlock()
	delete(vms.Metrics, pvUID)
}

func NewVolumeMetrics() *VolumeMetrics {
	return &VolumeMetrics{
		Metrics: make(map[types.UID]*VolumeMetric),
	}
}
