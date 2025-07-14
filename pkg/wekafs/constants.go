package wekafs

import (
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io/fs"
	"time"
)

const (
	// Garbage collection
	garbagePath = ".__internal__wekafs-async-delete"

	// Mounter
	MountBasePath = "/run/weka-fs-mounts/"

	// kubernetes configuration
	deviceID          = "deviceID"
	maxVolumeIdLength = 1920

	// Traces
	TracerName = "weka-csi"

	// SELinux
	selinuxContextWekaFs = "wekafs_csi_volume_t"
	selinuxContextNfs    = "nfs_t"

	// Mount Options
	MountOptionSyncOnClose              = "sync_on_close"
	MountOptionReadOnly                 = "ro"
	MountOptionWriteCache               = "writecache"
	MountOptionCoherent                 = "coherent"
	MountOptionForceDirect              = "forcedirect"
	MountOptionContainerName            = "container_name"
	MountOptionAcl                      = "acl"
	MountOptionNfsAsync                 = "async"
	MountOptionNfsSync                  = "async"
	MountOptionNfsHard                  = "hard"
	MountOptionNfsNoac                  = "noac"
	MountOptionNfsAc                    = "ac"
	MountOptionNfsRdirPlus              = "rdirplus"
	MountOptionReadCache                = "readcache"
	MountProtocolWekafs                 = "wekafs"
	MountProtocolNfs                    = "nfs"
	DefaultNfsMountOptions              = MountOptionNfsHard + "," + MountOptionNfsAsync + "," + MountOptionNfsRdirPlus
	ControlServerAdditionalMountOptions = MountOptionAcl + "," + MountOptionWriteCache
	NodeServerAdditionalMountOptions    = MountOptionWriteCache + "," + MountOptionSyncOnClose

	// Mount options persistency
	MountOptionsConfigMapNameTemplate = "%s-pv-mount-options"

	// Topology
	TopologyKeyNode              = "topology.wekafs.csi/node"
	TopologyLabelNodeGlobal      = "topology.csi.weka.io/node"
	TopologyLabelWekaGlobal      = "topology.csi.weka.io/global"
	TopologyLabelTransportGlobal = "topology.csi.weka.io/transport"

	TopologyLabelWekaLocalPattern = "topology.%s/accessible"
	TopologyLabelNodePattern      = "topology.%s/node"
	TopologyLabelTransportPattern = "topology.%s/transport"

	// WEKA client integration
	WekaKernelModuleName        = "wekafsgw"
	MaxSnapshotDeletionDuration = time.Hour * 2 // Max time to delete snapshot
	ProcModulesPath             = "/proc/modules"
	ProcWekafsInterface         = "/proc/wekafs/interface"
	SnapshotsSubDirectory       = ".snapshots"
	MaxHashLengthForObjectNames = 12            // Max length of hash for object names due to limitations of WEKA filesystem /snapshot names

	// Volume Backing types
	VolumeBackingTypeDirectory  VolumeBackingType = "DIRECTORY"
	VolumeBackingTypeFilesystem VolumeBackingType = "FILESYSTEM"
	VolumeBackingTypeSnapshot   VolumeBackingType = "SNAPSHOT"
	VolumeBackingTypeHybrid     VolumeBackingType = "HYBRID"

	// Volume Types (inside volumeHandle / volumeId)
	VolumeTypeDirV1         VolumeType = "dir/v1"  // if specified in storage class, create directory-backed volumes. FS name must be set in SC as well
	VolumeTypeUnified       VolumeType = "weka/v2" // no need to specify this in storageClass
	VolumeTypeUNKNOWN       VolumeType = "AMBIGUOUS_VOLUME_TYPE"
	VolumeTypeEmpty         VolumeType = ""
	SnapshotTypeUnifiedSnap            = "wekasnap/v2"

	// Data transports
	dataTransportNfs    DataTransport = "nfs"
	dataTransportWekafs DataTransport = "wekafs"

	// CSI Plugin Modes
	CsiModeNode          CsiPluginMode = "node"
	CsiModeController    CsiPluginMode = "controller"
	CsiModeAll           CsiPluginMode = "all"
	CsiModeMetricsServer CsiPluginMode = "metricsserver"

	DefaultVolumePermissions fs.FileMode = 0750
)

var (
	vendorVersion = "dev"

	ClusterApiNotFoundError = errors.New("could not get API client by cluster guid")
	KnownVolTypes           = [...]VolumeType{VolumeTypeDirV1, VolumeTypeUnified}

	TransportPreference = []DataTransport{dataTransportWekafs, dataTransportNfs}

	ErrFilesystemHasUnderlyingSnapshots = status.Errorf(codes.FailedPrecondition, "volume cannot be deleted since it has underlying snapshots")
	ErrFilesystemNotFound               = status.Errorf(codes.FailedPrecondition, "underlying filesystem was not found")

	ErrFilesystemBiggerThanRequested = errors.New("could not resize filesystem since it is already larger than requested size")
)
