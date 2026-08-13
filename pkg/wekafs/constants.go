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
	VolumeTypeDirV1   VolumeType = "dir/v1"  // if specified in storage class, create directory quotas (as in legacy CSI volumes). FS name must be set in SC as well
	VolumeTypeUnified VolumeType = "weka/v2" // no need to specify this in storageClass
	VolumeTypeUNKNOWN VolumeType = "AMBIGUOUS_VOLUME_TYPE"
	VolumeTypeEmpty   VolumeType = ""

	LegacySecretPath = "/legacy-volume-access"

	CsiModeNode       CsiPluginMode = "node"
	CsiModeController CsiPluginMode = "controller"
	CsiModeAll        CsiPluginMode = "all"
)

var DefaultVolumePermissions fs.FileMode = 0750

var KnownVolTypes = [...]VolumeType{VolumeTypeDirV1, VolumeTypeUnified}

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
	// capacityEnforcementParam is the StorageClass parameter naming the quota type, persisted
	// verbatim into a PersistentVolume's volumeAttributes at provisioning time.
	capacityEnforcementParam = "capacityEnforcement"

	volumeHealthyMessage = "volume exists on the Weka cluster and is reachable via the Weka API"

	// Values of the "condition" label on weka_csi_volume_health_conditions. Stable strings: a
	// dashboard or alert selects on them, so renaming one silently breaks whatever selects it.
	volumeConditionNoApiClient   = "no_api_client"
	volumeConditionNoQuota       = "no_quota"
	volumeConditionQuotaMismatch = "quota_mismatch"
	volumeConditionUnavailable   = "unavailable"

	// The conditions below are surfaced to whoever can read events in the volume's namespace, which
	// is not necessarily whoever administers the Weka cluster. They deliberately carry no filesystem
	// name and no path inside the filesystem: a PersistentVolume is cluster-scoped and a namespace
	// user cannot read one, so naming shared storage in a namespaced event would tell them something
	// they otherwise have no way to see. The identifiers are logged instead, where an administrator
	// reads them - see ProbeHealth.
	volumeFilesystemMissingMessage  = "the Weka filesystem backing this volume no longer exists"
	volumeFilesystemRemovingMessage = "the Weka filesystem backing this volume is being removed"
	volumePathMissingMessage        = "the volume's directory no longer exists on the Weka cluster"
	// volumeNoQuotaMessage is reported for a volume that exists but carries no quota. It is not an
	// abnormal condition - the volume works - but nothing enforces its capacity, so the condition
	// says so rather than calling it plainly healthy.
	volumeNoQuotaMessage = "volume exists on the Weka cluster, but has no quota, so its capacity is not enforced"
	// volumeNoApiClientMessage is reported for a volume the driver has no credentials for. Its
	// condition is unknown rather than bad - nothing is known about it at all - so this is only
	// used when an operator has asked for such volumes to be raised.
	volumeNoApiClientMessage = "volume has no Weka API credentials, so the driver cannot determine its condition, " +
		"enforce its capacity or expand it - reference an API secret from its StorageClass"

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
