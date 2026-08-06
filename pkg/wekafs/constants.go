/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package wekafs

import (
	"io/fs"
	"time"

	"github.com/wekafs/csi-wekafs/pkg/volumeid"
)

const (
	// LeaderStateDir is the directory where leader state files are stored
	LeaderStateDir = "/leader-state"
	// LeaderReadyFile signals to sidecars that this pod is the leader
	LeaderReadyFile = "/leader-state/leader_ready"
	// HealthProbePort is the port for the health probe endpoint
	HealthProbePort = "8081"
)

const (
	VolumeTypeDirV1   = volumeid.TypeDirV1   // if specified in storage class, create directory quotas (as in legacy CSI volumes). FS name must be set in SC as well
	VolumeTypeUnified = volumeid.TypeUnified // no need to specify this in storageClass
	VolumeTypeUNKNOWN = volumeid.TypeUnknown
	VolumeTypeEmpty   = volumeid.TypeEmpty

	LegacySecretPath = "/legacy-volume-access"

	CsiModeNode          CsiPluginMode = "node"
	CsiModeController    CsiPluginMode = "controller"
	CsiModeMetricsServer CsiPluginMode = "metricsserver"
)

// servesCsiGrpc reports whether this mode runs the CSI gRPC surface - the Identity/Controller/Node
// services, the socket they listen on, and the mounter behind them. Everything that follows from
// having no gRPC surface (no endpoint to validate, no socket to health-check, no leader-gated gRPC
// runnable to register) tests this one property, so it is named here rather than restated as a
// comparison against CsiModeMetricsServer at each site.
func (mode CsiPluginMode) servesCsiGrpc() bool {
	return mode != CsiModeMetricsServer
}

var DefaultVolumePermissions fs.FileMode = 0750

// KnownVolTypes aliases the list in pkg/volumeid rather than restating it, so that adding a
// volume type cannot leave the driver and the handle parser disagreeing about what exists.
var KnownVolTypes = volumeid.KnownTypes

const (
	deviceID                            = "deviceID"
	maxVolumeIdLength                   = 1920
	TracerName                          = "weka-csi"
	ControlServerAdditionalMountOptions = MountOptionAcl + "," + MountOptionWriteCache
	capacityRefreshInterval             = 5 * time.Second  // How often to refresh from K8s
	pendingReservationTTL               = 30 * time.Minute // Threshold to warn about stale pending reservations
)

const garbagePath = ".__internal__wekafs-async-delete"

const (
	// garbageCollectionTimeout bounds a single purge cycle so a hung mount cannot
	// block the detached GC goroutine indefinitely.
	garbageCollectionTimeout = 10 * time.Minute
	// garbageCollectionRetryBackoff delays a retry after a failed purge to avoid hot-looping.
	garbageCollectionRetryBackoff = time.Minute
)

const (
	dataTransportNfs    DataTransport = "nfs"
	dataTransportWekafs DataTransport = "wekafs"
)

const (
	VolumeBackingTypeDirectory  VolumeBackingType = "DIRECTORY"
	VolumeBackingTypeFilesystem VolumeBackingType = "FILESYSTEM"
	VolumeBackingTypeSnapshot   VolumeBackingType = "SNAPSHOT"
	VolumeBackingTypeHybrid     VolumeBackingType = "HYBRID"
	// VolumeBackingTypeUnknown is used when a request failed before its volume was constructed, so
	// the backing type was never determined.
	VolumeBackingTypeUnknown VolumeBackingType = "UNKNOWN"
)

const (
	selinuxContextWekaFs     = "wekafs_csi_volume_t"
	selinuxContextNfs        = "nfs_t"
	MountOptionSyncOnClose   = "sync_on_close"
	MountOptionReadOnly      = "ro"
	MountOptionWriteCache    = "writecache"
	MountOptionCoherent      = "coherent"
	MountOptionForceDirect   = "forcedirect"
	MountOptionContainerName = "container_name"
	MountOptionAcl           = "acl"
	MountOptionNfsAsync      = "async"
	MountOptionNfsSync       = "sync"
	MountOptionNfsHard       = "hard"
	MountOptionNfsNoac       = "noac"
	MountOptionNfsAc         = "ac"
	MountOptionNfsRdirPlus   = "rdirplus"
	MountOptionReadCache     = "readcache"
	MountProtocolWekafs      = "wekafs"
	MountProtocolNfs         = "nfs"
	DefaultNfsMountOptions   = MountOptionNfsHard + "," + MountOptionNfsAsync + "," + MountOptionNfsRdirPlus
)

const (

	// VolumeContextPodNameKey is the key in VolumeContext that describes the pod name
	VolumeContextPodNameKey = "csi.storage.k8s.io/pod.name"
	// VolumeContextPodNamespaceKey is the key in VolumeContext that describes the pod namespace
	VolumeContextPodNamespaceKey = "csi.storage.k8s.io/pod.namespace"
	// VolumeContextPvcNameKey is the key in VolumeContext that describes the PVC name
	VolumeContextPvcNameKey = "csi.storage.k8s.io/pvc/name"
	// VolumeContextPvcNamespaceKey is the key in VolumeContext that describes the PVC namespace
	VolumeContextPvcNamespaceKey = "csi.storage.k8s.io/pvc/namespace"

	// PodMountOptionOverrideAnnotation is the annotation key on pods for per-PVC mount option overrides.
	// Format: one entry per line (or separated by ';'):
	//   <pvc-name-regex>: <mount-option-modifiers>
	// Mount option modifiers are comma-separated with + (add) or - (remove) prefix
	// Example:
	//   my-volume-.*: -forcedirect, +readcache
	//   my-vol-1: -forcedirect, +readcache, +writecache
	//   my-vol-2: +inode_bits=64
	PodMountOptionOverrideAnnotation = "weka.io/mount-options-overrides"

	// PvcMountOptionOverrideAnnotation is the annotation key on PVCs for mount option overrides that apply to all pods mounting the PVC.
	// Format: same as PodMountOptionOverrideAnnotation but without the PVC name regex (applies to all pods).
	// Example:
	//   -forcedirect, +readcache
	PvcMountOptionOverrideAnnotation = "weka.io/mount-options-override"

	// Order of application:
	// 1. StorageClass default options
	// 2. Node Publish default options
	// 3. PvcMountOptionOverrideAnnotation
	// 4. PodMountOptionOverrideAnnotation (first matching pattern wins)
)

const (
	TopologyKeyNode              = "topology.wekafs.csi/node"
	TopologyLabelNodeGlobal      = "topology.csi.weka.io/node"
	TopologyLabelWekaGlobal      = "topology.csi.weka.io/global"
	TopologyLabelTransportGlobal = "topology.csi.weka.io/transport"

	TopologyLabelWekaLocalPattern = "topology.%s/accessible"
	TopologyLabelNodePattern      = "topology.%s/node"
	TopologyLabelTransportPattern = "topology.%s/transport"

	TopologyKeyZone   = "topology.kubernetes.io/zone"
	TopologyKeyRegion = "topology.kubernetes.io/region"

	WekaKernelModuleName             = "wekafsgw"
	NodeServerAdditionalMountOptions = MountOptionWriteCache + "," + MountOptionSyncOnClose
)

const (
	xattrCapacity   = "user.weka_capacity"
	xattrVolumeName = "user.weka_k8s_volname"
)

const (
	MaxSnapshotDeletionDuration = time.Hour * 2 // Max time to delete snapshot
)

const (
	SnapshotTypeUnifiedSnap = "wekasnap/v2"
	ProcModulesPath         = "/proc/modules"
)

const (
	MaxHashLengthForObjectNames = 12
	SnapshotsSubDirectory       = ".snapshots"
)

const (
	// pvIndexVolumeHandle names the field index that maps a CSI volume handle to its PersistentVolume.
	pvIndexVolumeHandle = "spec.csi.volumeHandle"
	// volumeSecretCacheTTL bounds how long a Secret read for a volume health check is reused. It
	// matches the chart's default health monitor interval, so a full sweep of the cluster costs
	// about one apiserver read per distinct Secret rather than one per volume.
	volumeSecretCacheTTL = 5 * time.Minute
	volumeHealthyMessage = "volume exists on the Weka cluster and is reachable via the Weka API"

	// listVolumesPageSize bounds a page when the CO does not set max_entries. Pages are served from
	// the reconciler's cache, so this is only about response size, not about how much work one call
	// may do.
	listVolumesPageSize = 100
	// listVolumesTokenPrefix marks a pagination cursor as this driver's own.
	listVolumesTokenPrefix = "wekafs:v1:"
)

const (
	// volumeHealthReconcileInterval is how long the reconciler waits between finishing one sweep of
	// every volume and starting the next. A sweep that overruns simply delays the next one.
	volumeHealthReconcileInterval = 5 * time.Minute
	// volumeHealthProbeConcurrency bounds how many volumes the reconciler probes at once, and so
	// caps the sustained Weka API call rate it can produce.
	volumeHealthProbeConcurrency = 10
	// volumeHealthMaxAge is how long a probe result may be served before it is reported as unknown
	// instead. Generous relative to the interval, so a slow or partially failing sweep degrades to
	// stale-but-useful rather than blanking every condition at once.
	volumeHealthMaxAge = 30 * time.Minute
)

const (
	inactiveMountGcPeriod = time.Minute * 10
)

// TransportPreference is the order GetPreferredMounter walks when the caller has no transport of its
// own to honour. Native wekafs comes first: NFS exists as a fallback for hosts without a working
// Weka client, not as an equal alternative.
var TransportPreference = []DataTransport{dataTransportWekafs, dataTransportNfs}
