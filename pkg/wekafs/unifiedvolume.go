package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/pkg/xattr"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const (
	MaxHashLengthForObjectNames = 12
	SeedSnapshotPrefix          = "csi-seed-snap-"
)

var ErrNoXattrOnVolume = errors.New("xattr not set on volume")
var ErrBadXattrOnVolume = errors.New("could not parse xattr on volume")

// UnifiedVolume is a volume object representation, not necessarily instantiated (e.g. can exist or not exist)
type UnifiedVolume struct {
	id                  string
	FilesystemName      string
	filesystemGroupName string
	SnapshotName        string
	SnapshotAccessPoint string
	SnapshotUuid        *uuid.UUID
	innerPath           string
	apiClient           *apiclient.ApiClient
	permissions         fs.FileMode
	ownerUid            int
	ownerGid            int
	mounter             *wekaMounter // TODO: could be removed in favor of volume.server.getMounter(), but should we?
	mountPath           map[bool]string
	enforceCapacity     bool

	srcVolume   Volume
	srcSnapshot Snapshot

	server AnyServer
}

func (v *UnifiedVolume) MarshalZerologObject(e *zerolog.Event) {

	e.Str("id", v.id).
		Str("filesystem", v.FilesystemName).
		Str("group_name", v.filesystemGroupName).
		Str("snapshot_name", v.SnapshotName).
		Str("snapshot_access_point", v.SnapshotAccessPoint).
		Str("inner_path", v.innerPath)

	if v.srcVolume != nil {
		srcVolID := v.srcVolume.GetId()
		e.Str("source_volume_id", srcVolID)
	}

	if v.srcSnapshot != nil {
		srcSnapID := v.srcSnapshot.GetId()
		e.Str("source_snapshot_id", srcSnapID)
	}
}

func (v *UnifiedVolume) requiresGc() bool {
	return v.hasInnerPath() && !v.isOnSnapshot()
}

// isOnSnapshot returns true if volume is located on snapshot, regardless if in root directory or under innerPath
func (v *UnifiedVolume) isOnSnapshot() bool {
	// we already have a snapshot ID
	if v.SnapshotUuid != nil {
		return true
	}
	// snapshot may not yet exist, but volume is snap based
	if v.SnapshotAccessPoint != "" {
		return true
	}
	return false
}

// hasInnerPath returns true for volumes having innerPath (basically either legacy directory OR directory on snapshot)
func (v *UnifiedVolume) hasInnerPath() bool {
	return v.getInnerPath() != ""
}

// isFilesystem returns true if the volume is an FS (not snapshot or directory)
func (v *UnifiedVolume) isFilesystem() bool {
	return !v.isOnSnapshot() && !v.hasInnerPath()
}

// hasUnderlyingSnapshots returns True if volume is a FS (not its snapshot) and has any weka snapshots beneath it
func (v *UnifiedVolume) hasUnderlyingSnapshots(ctx context.Context) (bool, error) {
	logger := log.Ctx(ctx)
	has := false

	if v.isOnSnapshot() {
		return false, nil
	}
	snapshots, err := v.getUnderlyingSnapshots(ctx)
	if err != nil {
		return true, err
	}
	if snapshots == nil {
		return false, nil
	}
	if len(*snapshots) > 0 {
		seedSnapshotName := v.getSeedSnapshotName()
		for _, s := range *snapshots {
			if s.IsRemoving || s.Name == seedSnapshotName {
				continue
			}
			logger.Debug().Str("snapshot", s.Name).Str("snapshot_access_point", s.AccessPoint).Msg("Existing snapshot prevents filesystem from deletion")
			has = true
			return has, nil
		}
	}
	return has, nil
}

// isAllowedForDeletion returns true if volume can be deleted (basically all cases besides FS volume having Weka snapshots)
func (v *UnifiedVolume) isAllowedForDeletion(ctx context.Context) bool {
	if !v.isFilesystem() {
		return true
	} else {
		hasSnaps, err := v.hasUnderlyingSnapshots(ctx)
		if hasSnaps || err != nil {
			return false
		}
	}
	return true
}

// getUnderlyingSnapshots intended to return list of Weka snapshots that exist for filesystem
func (v *UnifiedVolume) getUnderlyingSnapshots(ctx context.Context) (*[]apiclient.Snapshot, error) {
	snapshots := &[]apiclient.Snapshot{}
	if v.apiClient == nil {
		return nil, errors.New("cannot check for underlying snaphots as volume is not bound to API")
	}
	fsObj, err := v.getFilesystemObj(ctx)
	if err != nil {
		return nil, err
	}
	if fsObj == nil {
		return nil, errors.New("could not fetch volume snapshots")
	}

	err = v.apiClient.FindSnapshotsByFilesystem(ctx, fsObj, snapshots)
	if err != nil {
		return nil, err
	}
	return snapshots, nil
}

func (v *UnifiedVolume) getSeedSnapshotName() string {
	return generateWekaSeedSnapshotName(SeedSnapshotPrefix, v.FilesystemName)
}

func (v *UnifiedVolume) getSeedSnapshotAccessPoint() string {
	return generateWekaSeedAccessPoint(v.FilesystemName)
}

// UpdateParams updates params on volume upon creation. Was part of Create initially, but must be done after content source is applied
func (v *UnifiedVolume) UpdateParams(ctx context.Context) error {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()

	// need to set permissions, for this have to mount as root and change ownership
	const xattrMount = false // no need to have xattr mount to do that
	err, unmount := v.opportunisticMount(ctx, xattrMount)
	defer unmount()
	if err != nil {
		return err
	}

	// make sure we don't hit umask upon setting permissions
	oldMask := syscall.Umask(0)
	defer syscall.Umask(oldMask)

	volPath := v.getFullPath(ctx, xattrMount)
	if err := os.Chmod(volPath, v.permissions); err != nil {
		logger.Error().Err(err).Str("full_path", volPath).Msg("Failed to change directory")
		return err
	}

	// Update volume UID/GID if set in storage class
	if v.ownerUid+v.ownerGid != 0 {
		logger.Trace().Int("owner_uid", v.ownerUid).Int("owner_gid", v.ownerGid).Msg("Setting volume ownership")
		if err := os.Chown(volPath, v.ownerUid, v.ownerGid); err != nil {
			return err
		}
	}
	return nil
}

// getFilesystemFreeSpace returns maximum capacity that can be obtained on filesystem, e.g. disk free (for directory volumes)
func (v *UnifiedVolume) getFilesystemFreeSpace(ctx context.Context) (int64, error) {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	const xattrMount = true // need to have xattr mount to do that
	err, unmount := v.opportunisticMount(ctx, xattrMount)
	defer unmount()
	if err != nil {
		return 0, err
	}
	logger.Trace().Str("filesystem", v.FilesystemName).Msg("Fetching max available capacity on filesystem")

	var stat syscall.Statfs_t
	mountPath := v.mountPath[xattrMount]
	err = syscall.Statfs(mountPath, &stat)
	if err != nil {
		return -1, status.Errorf(codes.FailedPrecondition, "Could not obtain free capacity on mount path %s: %s", mountPath, err.Error())
	}
	// Available blocks * size per block = available space in bytes
	maxCapacity := int64(stat.Bavail * uint64(stat.Bsize))
	logger.Debug().Int64("max_capacity", maxCapacity).Msg("Success")
	return maxCapacity, nil
}

// getFreeSpaceOnStorage returns maximum capacity on storage (for either creating new or resizing an FS)
func (v *UnifiedVolume) getFreeSpaceOnStorage(ctx context.Context) (int64, error) {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	existsOk, err := v.Exists(ctx)
	if err != nil {
		return -1, status.Errorf(codes.Internal, "Failed to check for existence of volume")
	}

	logger.Trace().Bool("filesystem_exists", existsOk).Str("filesystem", v.FilesystemName).Msg("Attempting to get max capacity for filesystem placement")
	if v.apiClient == nil {
		return -1, status.Errorf(codes.FailedPrecondition, "Could not bind volume %s to API endpoint", v.FilesystemName)
	}
	maxCapacity, err := v.apiClient.GetFreeCapacity(ctx)
	if err != nil {
		return -1, status.Errorf(codes.FailedPrecondition, "Could not obtain free capacity for filesystem %s on cluster %s: %s", v.GetId(), v.apiClient.ClusterName, err.Error())
	}
	logger.Debug().Uint64("max_capacity", maxCapacity).Msg("Resolved free capacity")
	return int64(maxCapacity), nil
}

// getFilesystemTotalCapacity returns maximum capacity that can be obtained by snapshot without resizing the FS, e.g. FS size
func (v *UnifiedVolume) getFilesystemTotalCapacity(ctx context.Context) (int64, error) {
	fsObj, err := v.getFilesystemObj(ctx)
	if err != nil {
		return -1, status.Errorf(codes.FailedPrecondition, "Could not obtain free capacity for filesystem %s on cluster %s: %s", v.GetId(), v.apiClient.ClusterName, err.Error())
	}
	if fsObj != nil {
		return fsObj.TotalCapacity, nil
	}
	return int64(apiclient.MaxQuotaSize), nil
}

func (v *UnifiedVolume) getMaxCapacity(ctx context.Context) (int64, error) {
	var maxCapacity int64 = 0

	if v.isOnSnapshot() {
		// this is a snapshot volume, no matter
		maxFsSize, err1 := v.getFilesystemTotalCapacity(ctx)
		maxFreeCapacity, err2 := v.getFreeSpaceOnStorage(ctx)
		if err1 == nil && err2 == nil {
			maxCapacity = Min(maxFsSize, maxFreeCapacity)
		} else if err1 != nil {
			return -1, err1
		} else {
			return -1, err2
		}
	}
	if v.hasInnerPath() {
		maxFreeSpaceOnFs, err := v.getFilesystemFreeSpace(ctx)
		if err != nil {
			return -1, err
		}
		if maxCapacity > 0 {
			maxCapacity = Min(maxCapacity, maxFreeSpaceOnFs)
		} else {
			maxCapacity = maxFreeSpaceOnFs
		}
	} else {
		maxFsSize, err := v.getFreeSpaceOnStorage(ctx)
		if err != nil {
			return -1, err
		}
		return maxFsSize, nil
	}
	return maxCapacity, nil
}

func (v *UnifiedVolume) GetType() VolumeType {
	return VolumeTypeUnified
}

func (v *UnifiedVolume) getCapacityFromQuota(ctx context.Context) (int64, error) {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	const xattrMount = true // need to have xattr mount to do that
	err, unmount := v.opportunisticMount(ctx, xattrMount)
	defer unmount()
	if err != nil {
		return 0, err
	}

	if v.apiClient != nil && v.apiClient.SupportsQuotaDirectoryAsVolume() && v.mounter.debugPath != "" {
		size, err := v.getSizeFromQuota(ctx)
		if err == nil {
			logger.Debug().Uint64("current_capacity", size).Str("capacity_source", "quota").Msg("Resolved current capacity")
			return int64(size), nil
		}
	}
	logger.Trace().Msg("Volume appears to be a legacy volume, failing back to Xattr")
	size, err := v.getSizeFromXattr(ctx)
	if err != nil {
		return 0, err
	}
	logger.Debug().Uint64("current_capacity", size).Str("capacity_source", "xattr").Msg("Resolved current capacity")
	return int64(size), nil
}

func (v *UnifiedVolume) getCapacityFromFsSize(ctx context.Context) (int64, error) {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	fsObj, err := v.getFilesystemObj(ctx)
	if err != nil {
		return -1, err
	}
	size := fsObj.TotalCapacity
	if size > 0 {
		logger.Debug().Int64("current_capacity", size).Str("capacity_source", "filesystem").Msg("Resolved current capacity")
		return size, nil
	}
	return size, nil
}

func (v *UnifiedVolume) GetCapacity(ctx context.Context) (int64, error) {
	capacityFromQuota, err1 := v.getCapacityFromQuota(ctx)
	capacityFromFsSise, err2 := v.getCapacityFromFsSize(ctx)
	if err1 == nil && err2 == nil {
		return Min(capacityFromFsSise, capacityFromQuota), nil
	} else if err1 != nil {
		return capacityFromFsSise, nil
	} else {
		// TODO: naive implementation, should review as we must have both errors
		return capacityFromQuota, nil
	}
}

func (v *UnifiedVolume) resizeFilesystem(ctx context.Context, capacity int64) error {
	fsObj, err := v.getFilesystemObj(ctx)
	if err != nil {
		return err
	}

	capLimit := capacity

	fsu := apiclient.NewFileSystemResizeRequest(fsObj.Uid, &capLimit)
	err = v.apiClient.UpdateFileSystem(ctx, fsu, fsObj)
	return err
}

func (v *UnifiedVolume) ensureSufficientFsSizeOnUpdateCapacity(ctx context.Context, capacityLimit int64) error {
	// check if we need to resize filesystem actually for snapshot volume as otherwise user might hit limits regardless of quota
	// this is important for all types of volumes (FS, FSSNAP, Dir)
	// NOTE1: but for DirVolume we still can't ensure user will not hit limits due to sharing single FS / SNAP between multiple DirVolumes
	// NOTE2: this cannot be done without API
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()

	if v.apiClient == nil {
		logger.Trace().Msg("Volume is not bound to API client, expansion is not possible")
		return nil
	} else {

	}
	currentFsCapacity, err := v.getCapacityFromFsSize(ctx)
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, "Failed to get current volume capacity for volume %s", v.GetId())
	}
	if currentFsCapacity < capacityLimit {
		if (v.isOnSnapshot() || v.hasInnerPath()) && (v.server != nil && !v.server.(*ControllerServer).allowAutoFsExpansion) {
			return status.Errorf(codes.FailedPrecondition, "Not allowed to expand volume of %s as underlying filesystem %s is too small", v.GetType(), v.FilesystemName)
		}
		logger.Debug().Str("filesystem", v.FilesystemName).Int64("desired_capacity", capacityLimit).Msg("New volume size doesn't fit current filesystem limits, expanding filesystem")
		err := v.resizeFilesystem(ctx, capacityLimit)
		if err != nil {
			logger.Error().Err(err).Msg("Failed to expand filesystem to support new volume capacity")
			return status.Errorf(codes.FailedPrecondition, "Could not expand filesystem to support new volume capacity")
		}
		// TODO: Add actual FS resize and err handling
	}
	return nil
}

func (v *UnifiedVolume) UpdateCapacity(ctx context.Context, enforceCapacity *bool, capacityLimit int64) error {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	// check if required AND possible to expand filesystem, expand if needed or fail
	if err := v.ensureSufficientFsSizeOnUpdateCapacity(ctx, capacityLimit); err != nil {
		return err
	}

	// update capacity of the volume by updating quota object on its root directory (or XATTR)
	logger.Info().Int64("desired_capacity", capacityLimit).Msg("Updating volume capacity")
	var fallback = true // whether we should try to use xAttr fallback or not
	f := func() error { return v.updateCapacityQuota(ctx, enforceCapacity, capacityLimit) }
	if v.apiClient == nil {
		logger.Trace().Msg("Volume has no API client bound, updating capacity in legacy mode")
		f = func() error { return v.updateCapacityXattr(ctx, enforceCapacity, capacityLimit) }
		fallback = false
	} else if !v.apiClient.SupportsQuotaDirectoryAsVolume() {
		logger.Warn().Msg("Updating quota via API not supported by Weka cluster, updating capacity in legacy mode")
		f = func() error { return v.updateCapacityXattr(ctx, enforceCapacity, capacityLimit) }
		fallback = false
	} else if !v.apiClient.SupportsAuthenticatedMounts() && v.apiClient.Credentials.Organization != "Root" {
		logger.Warn().Msg("Updating quota via API is not supported by Weka cluster since filesystem is located in non-default organization, updating capacity in legacy mode")
		f = func() error { return v.updateCapacityXattr(ctx, enforceCapacity, capacityLimit) }
		fallback = false
	} else if v.mounter.debugPath != "" {
		logger.Trace().Msg("Updating quota via API is not possible since running in debug mode")
		f = func() error { return v.updateCapacityXattr(ctx, enforceCapacity, capacityLimit) }
		fallback = false
	}
	err := f()
	if err == nil {
		logger.Info().Int64("new_capacity", capacityLimit).Msg("Successfully updated capacity for volume")
		return err
	}
	if fallback {
		logger.Warn().Err(err).Msg("Failed to set quota via API, failing back to xattr")
		f = func() error { return v.updateCapacityXattr(ctx, enforceCapacity, capacityLimit) }
		err := f()
		if err != nil {
			logger.Error().Err(err).Msg("Failed to set capacity even in failback mode")
		}
		return err
	}
	return err
}

func (v *UnifiedVolume) updateCapacityQuota(ctx context.Context, enforceCapacity *bool, capacityLimit int64) error {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	enfCapacity := "RETAIN"
	if enforceCapacity != nil {
		if *enforceCapacity {
			enfCapacity = "STRICT"
		} else {
			enfCapacity = "PERMISSIVE"
		}
	}
	logger.Debug().Str("capacity_enforcement", enfCapacity).Msg("Updating quota for volume")
	inodeId, err := v.getInodeId(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to fetch inode ID for volume")
		return err
	}
	fsObj, err := v.getFilesystemObj(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to fetch filesystem object of the volume")
		return err
	}

	// check if the quota already exists. If not - create it and exit
	_, err = v.apiClient.GetQuotaByFileSystemAndInode(ctx, fsObj, inodeId)
	if err != nil {
		if err == apiclient.ObjectNotFoundError {
			logger.Trace().Uint64("inode_id", inodeId).Msg("No quota entry for inode ID")
		} else {
			logger.Error().Err(err).Uint64("inode_id", inodeId).Msg("Failed to fetch quota object of the volume")
			return status.Error(codes.Internal, err.Error())
		}
	}

	_, err = v.setQuota(ctx, enforceCapacity, uint64(capacityLimit))
	return err

}

func (v *UnifiedVolume) updateCapacityXattr(ctx context.Context, enforceCapacity *bool, capacityLimit int64) error {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	const xattrMount = true // must have xattrs for this case
	if !v.isMounted(ctx, xattrMount) {
		err, unmountFunc := v.Mount(ctx, xattrMount)
		if err != nil {
			return err
		} else {
			defer unmountFunc()
		}
	}

	logger.Trace().Int64("desired_capacity", capacityLimit).Msg("Updating xattrs on volume")
	if enforceCapacity != nil && *enforceCapacity {
		logger.Warn().Msg("Legacy volume does not support enforce capacity")
	}
	err := setVolumeProperties(v.getFullPath(ctx, xattrMount), capacityLimit, v.innerPath)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to update xattrs on volume, capacity is not set")
	}
	return err
}

func (v *UnifiedVolume) moveToTrash(ctx context.Context) error {
	if v.requiresGc() {
		v.mounter.gc.triggerGcVolume(ctx, *v)
		return nil
	}
	go func() {
		err := v.Delete(ctx)
		if err != nil {
			log.Ctx(ctx).Error().Err(err).Msg("Failed to delete filesystem")
		}
	}()
	return nil
}

func (v *UnifiedVolume) getInnerPath() string {
	return v.innerPath
}

func (v *UnifiedVolume) getFullPath(ctx context.Context, xattr bool) string {
	mountParts := []string{v.mountPath[xattr]}
	if v.isOnSnapshot() {
		mountParts = append(mountParts, []string{".snapshots", v.SnapshotAccessPoint}...) //TODO: validate when and how it is populated
	}
	if v.hasInnerPath() {
		mountParts = append(mountParts, v.getInnerPath())
	}
	fullPath := filepath.Join(mountParts...)
	return fullPath
}

//getInodeId used for obtaining the mount Path inode ID (to set quota on it later)
func (v *UnifiedVolume) getInodeId(ctx context.Context) (uint64, error) {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	const xattrMount = false // no need to have xattr mount to do that
	err, unmount := v.opportunisticMount(ctx, xattrMount)
	defer unmount()
	if err != nil {
		return 0, err
	}

	fullPath := v.getFullPath(ctx, xattrMount)
	logger.Trace().Str("full_path", fullPath).Msg("Getting root inode of volume")
	fileInfo, err := os.Stat(fullPath)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to get inode")
		return 0, err
	}
	stat, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !ok {
		logger.Error().Msg("Failed to call stat on inode")
		return 0, errors.New(fmt.Sprintf("failed to obtain inodeId from %s", v.mountPath[xattrMount]))
	}
	logger.Debug().Uint64("inode_id", stat.Ino).Msg("Succesfully fetched root inode ID")
	return stat.Ino, nil
}

// GetId returns the ID of the volume
func (v *UnifiedVolume) GetId() string {
	return v.id
}

// setQuota creates a quota object for the volume. set for every type of volume including root of FS
func (v *UnifiedVolume) setQuota(ctx context.Context, enforceCapacity *bool, capacityLimit uint64) (*apiclient.Quota, error) {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	var quotaType apiclient.QuotaType
	if enforceCapacity != nil {
		if !*enforceCapacity {
			quotaType = apiclient.QuotaTypeSoft
		} else {
			quotaType = apiclient.QuotaTypeHard
		}
	} else {
		quotaType = apiclient.QuotaTypeDefault
	}
	logger.Trace().Uint64("desired_capacity", capacityLimit).Str("quotaType", string(quotaType)).Msg("Creating a quota for volume")

	fsObj, err := v.getFilesystemObj(ctx)
	if err != nil {
		return nil, err
	}
	inodeId, err := v.getInodeId(ctx)
	if err != nil {
		return nil, errors.New("cannot set quota, could not find inode ID of the volume")
	}
	qr := apiclient.NewQuotaCreateRequest(*fsObj, inodeId, quotaType, capacityLimit)
	q := &apiclient.Quota{}
	if err := v.apiClient.CreateQuota(ctx, qr, q, true); err != nil {
		return nil, err
	}
	logger.Debug().Msg("Quota successfully set for volume")
	return q, nil
}

// getQuota returns quota object for volume (if exists) or error
func (v *UnifiedVolume) getQuota(ctx context.Context) (*apiclient.Quota, error) {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	logger.Trace().Msg("Getting existing quota for volume")
	fsObj, err := v.getFilesystemObj(ctx)
	if err != nil {
		return nil, err
	}
	inodeId, err := v.getInodeId(ctx)
	if err != nil {
		return nil, err
	}
	ret, err := v.apiClient.GetQuotaByFileSystemAndInode(ctx, fsObj, inodeId)
	if ret != nil {
		logger.Trace().Interface("quota_type", ret.GetQuotaType()).Uint64("current_capacity", ret.GetCapacityLimit()).Msg("Successfully acquired existing quota for volume")
	}
	return ret, err
}

// getSizeFromQuota returns volume size from quota object (if exists) or capacity limit if quota is not set
func (v *UnifiedVolume) getSizeFromQuota(ctx context.Context) (uint64, error) {
	q, err := v.getQuota(ctx)
	if err != nil {
		return 0, err
	}
	if q != nil {
		return q.GetCapacityLimit(), nil
	}
	return 0, errors.New("could not fetch quota from API")
}

// getSizeFromXattr returns volume size from extended attributes, mostly fallback for very old pre-API Weka clusters
func (v *UnifiedVolume) getSizeFromXattr(ctx context.Context) (uint64, error) {
	const xattrMount = true // need to have xattr mount to do that
	err, unmount := v.opportunisticMount(ctx, xattrMount)
	defer unmount()
	if err != nil {
		return 0, err
	}

	if capacityString, err := xattr.Get(v.getFullPath(ctx, xattrMount), xattrCapacity); err == nil {
		if capacity, err := strconv.ParseInt(string(capacityString), 10, 64); err == nil {
			return uint64(capacity), nil
		}
		return 0, ErrBadXattrOnVolume
	}
	return 0, ErrNoXattrOnVolume
}

// getFilesystemObj returns the Weka filesystem object
func (v *UnifiedVolume) getFilesystemObj(ctx context.Context) (*apiclient.FileSystem, error) {
	if v.apiClient == nil {
		return nil, errors.New("cannot get object of API-unbound volume")
	}
	fsObj, err := v.apiClient.GetFileSystemByName(ctx, v.FilesystemName)
	if err != nil {
		if err == apiclient.ObjectNotFoundError {
			return nil, nil
		}
		return nil, err
	}
	return fsObj, nil
}

func (v *UnifiedVolume) getSnapshotObj(ctx context.Context) (*apiclient.Snapshot, error) {
	if v.apiClient == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Could not bind snapshot %s to API endpoint", v.GetId())
	}
	snap := &apiclient.Snapshot{}
	if v.SnapshotUuid != nil {
		err := v.apiClient.GetSnapshotByUid(ctx, *v.SnapshotUuid, snap)
		return snap, err
	}
	return nil, nil // no snapshot found
}

// Mount creates a mount using specific options (currently only xattr true/false) and increases reference to it
// returns UmnountFunc that can be executed to decrese refCount / unmount
// NOTE: it always mounts only the filesystem directly. Any navigation inside should be done on the mount
func (v *UnifiedVolume) Mount(ctx context.Context, xattr bool) (error, UnmountFunc) {
	if v.mounter == nil {
		return errors.New("could not mount volume, mounter not in context"), func() {}
	}
	if xattr {
		mountXattr, err, unmountFunc := v.mounter.MountXattr(ctx, v.FilesystemName, v.apiClient)
		if err == nil {
			v.mountPath[xattr] = mountXattr
		}
		return err, unmountFunc
	} else {
		mount, err, unmountFunc := v.mounter.Mount(ctx, v.FilesystemName, v.apiClient)
		if err == nil {
			v.mountPath[xattr] = mount
		}
		return err, unmountFunc
	}
}

// Unmount decreases refCount / unmounts volume using specific mount options, currently only xattr true/false
func (v *UnifiedVolume) Unmount(ctx context.Context, xattr bool) error {
	if v.mounter == nil {
		Die("Volume unmount could not be done since mounter not defined on it")
	}
	if xattr {
		err := v.mounter.Unmount(ctx, v.FilesystemName)
		if err == nil {
			v.mountPath[xattr] = ""
		}
		return err
	} else {
		err := v.mounter.UnmountXattr(ctx, v.FilesystemName)
		if err == nil {
			v.mountPath[xattr] = ""
		}
		return err
	}
}

// opportunisticMount used mostly in functions that require a short mount and unmount immediately.
//in such case, we are not increasing refCount. Just less logging, and avoidance of redundant unmount / mount
// the function also returns the opposite unmount function
func (v *UnifiedVolume) opportunisticMount(ctx context.Context, xattr bool) (err error, unmountFunc func()) {
	if !v.isMounted(ctx, xattr) {
		err, unmountFunc := v.Mount(ctx, xattr)
		if err != nil {
			return err, func() {}
		}
		return nil, unmountFunc
	}
	return nil, func() {}
}

// Exists returns true if the actual data representing volume object exists,e.g. filesystem, snapshot and innerPath
func (v *UnifiedVolume) Exists(ctx context.Context) (bool, error) {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	if (v.isOnSnapshot() || v.isFilesystem()) && v.apiClient == nil {
		logger.Error().Msg("No API bound, assuming volume does not exist")
		return false, nil
	}
	if v.isFilesystem() {
		return v.fileSystemExists(ctx)
	}
	if v.isOnSnapshot() {
		exists, err := v.snapshotExists(ctx)
		if err != nil {
			logger.Error().Err(err).Str("snapshot", v.SnapshotName).Msg("Failed to fetch snapshot")
			return false, err
		}
		if !exists {
			logger.Trace().Str("snapshot", v.SnapshotName).Msg("Snapshot does not exist on storage")
		}
	}
	if v.hasInnerPath() {
		const xattrMount = false // no need to mount with xattr for this
		err, unmount := v.opportunisticMount(ctx, xattrMount)
		defer unmount()
		if err != nil {
			return false, err
		}

		if !PathExists(v.getFullPath(ctx, xattrMount)) {
			logger.Trace().Str("filesystem", v.FilesystemName).Msg("Volume not found on filesystem")
			return false, nil
		}
		if err := pathIsDirectory(v.getFullPath(ctx, xattrMount)); err != nil {
			logger.Error().Err(err).Str("full_path", v.getFullPath(ctx, xattrMount)).Msg("Volume is unusable: path is not a directory")
			return false, status.Error(codes.Internal, err.Error())
		}
		logger.Debug().Str("full_path", v.getFullPath(ctx, xattrMount)).Msg("Volume exists and is accessible")
		return true, nil
	}
	return true, nil
}

func (v *UnifiedVolume) ExistsAndMatchesCapacity(ctx context.Context, capacity int64) (bool, bool, error) {
	exists, err := v.Exists(ctx)
	if err != nil || !exists {
		return exists, false, err
	}
	reportedCapacity, err := v.GetCapacity(ctx)
	if err != nil {
		return true, false, err
	}
	matches := reportedCapacity == capacity
	return exists, matches, err

}

func (v *UnifiedVolume) fileSystemExists(ctx context.Context) (bool, error) {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	logger.Trace().Msg("Checking if volume exists")
	fsObj, err := v.getFilesystemObj(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to fetch volume from underlying storage")
		return false, err
	}
	if fsObj == nil || fsObj.Uid == uuid.Nil {
		logger.Debug().Msg("Volume does not exist")
		return false, err
	}
	if fsObj.IsRemoving {
		logger.Debug().Msg("Volume exists but is in removing state")
		return false, nil
	}
	logger.Info().Msg("Volume exists")
	return true, nil
}

func (v *UnifiedVolume) snapshotExists(ctx context.Context) (bool, error) {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	logger.Trace().Msg("Checking if volume exists")
	snapObj, err := v.getSnapshotObj(ctx)
	if err != nil {
		logger.Error().Err(err).Str("snapshot", v.SnapshotName).Msg("Failed to fetch volume from underlying storage")
		return false, err
	}
	if snapObj == nil || snapObj.Uid == uuid.Nil {
		logger.Trace().Msg("Volume does not exist")
		return false, err
	}
	if snapObj.IsRemoving {
		logger.Debug().Msg("Volume exists but is in removing state")
		return false, nil
	}
	logger.Debug().Msg("Volume exists")
	return true, nil
}

// isFilesystemEmpty returns true if the filesystem root directory is empty (excluding .snapshots directory)
func (v *UnifiedVolume) isFilesystemEmpty(ctx context.Context) (bool, error) {
	err, umount := v.opportunisticMount(ctx, false)
	if err != nil {
		return false, err
	}
	defer umount()

	dir, err := os.Open(v.getFullPath(ctx, false))
	if err != nil {
		return false, err
	}
	defer func() { _ = dir.Close() }()

	fileNames, err := dir.Readdirnames(2)
	if err == io.EOF {
		return true, nil
	}
	for _, name := range fileNames {
		if name == ".snapshots" {
			continue
		}
		return false, nil
	}
	return true, nil
}

func (v *UnifiedVolume) createSeedSnapshot(ctx context.Context) error {
	seedName := v.getSeedSnapshotName()
	seedAccessPoint := v.getSeedSnapshotAccessPoint()
	ctx = log.Ctx(ctx).With().
		Str("seed_snapshot_name", seedName).
		Str("seed_access_point", seedAccessPoint).
		Str("filesystem", v.FilesystemName).
		Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	fsObj, err := v.getFilesystemObj(ctx)
	if err != nil {
		return err
	}
	r, err := apiclient.NewSnapshotCreateRequest(seedName, seedAccessPoint, fsObj.Uid, nil, false)
	if err != nil {
		return err
	}
	snapObj := &apiclient.Snapshot{}
	logger.Trace().Msg("Creating seed snapshot")
	err = v.apiClient.CreateSnapshot(ctx, r, snapObj)
	if err != nil {
		log.Error().Err(err).Msg("")
	}
	return err
}

func (v *UnifiedVolume) deleteSeedSnapshot(ctx context.Context) {
	logger := log.Ctx(ctx)
	snapObj, err := v.getSeedSnapshot(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to fetch seed snapshot, skipping its cleanup")
	}
	if snapObj != nil && snapObj.Uid != uuid.Nil {
		dr := &apiclient.SnapshotDeleteRequest{Uid: snapObj.Uid}
		logger.Trace().Msg("Scheduling background deletion of seed snapshot")
		go func() {
			if err := v.apiClient.DeleteSnapshot(ctx, dr); err != nil {
				logger.Error().Err(err).Msg("Failed to delete seed snapshot")
			}
		}()
	} else {
		logger.Trace().Msg("No seed snapshot was found for filesystem")
	}
}

func (v *UnifiedVolume) getSeedSnapshot(ctx context.Context) (*apiclient.Snapshot, error) {
	snapObj, err := v.apiClient.GetSnapshotByName(ctx, v.getSeedSnapshotName())
	if err != nil {
		return nil, err
	}
	if snapObj == nil || snapObj.Uid == uuid.Nil {
		return nil, nil
	}
	if snapObj.AccessPoint != v.getSeedSnapshotAccessPoint() {
		return nil, errors.New("mismatch detected between seed snapshot identifiers")
	}
	if snapObj.IsWritable {
		return nil, errors.New("seed snapshot is writable")
	}
	return snapObj, nil
}

func (v *UnifiedVolume) hasSeedSnapshot(ctx context.Context) bool {
	snapObj, err := v.getSeedSnapshot(ctx)
	return err == nil && snapObj != nil && snapObj.Uid != uuid.Nil
}

func (v *UnifiedVolume) ensureSeedSnapshot(ctx context.Context) error {
	logger := log.Ctx(ctx)
	if v.hasSeedSnapshot(ctx) {
		return nil
	}
	logger.Debug().Msg("Ensuring seed snapshot exists for filesystem")
	empty, err := v.isFilesystemEmpty(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("")
	}
	if empty {
		return v.createSeedSnapshot(ctx)
	}
	return errors.New("cannot create seed snaspshot on non-empty filesystem")
}

// Create actually creates the storage location for the particular volume object
func (v *UnifiedVolume) Create(ctx context.Context, capacity int64) error {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	// validate minimum capacity before create new volume
	maxStorageCapacity, err := v.getMaxCapacity(ctx)
	logger.Trace().Int64("max_capacity", maxStorageCapacity).Msg("Validating enough capacity on storage for creating the volume")
	if err != nil {
		return status.Errorf(codes.Internal, fmt.Sprintf("CreateVolume: Cannot obtain free capacity for volume %s", v.GetId()))
	}
	if capacity > maxStorageCapacity {
		return status.Errorf(codes.OutOfRange, fmt.Sprintf("Requested capacity %d exceeds maximum allowed %d", capacity, maxStorageCapacity))
	}
	if v.isFilesystem() {
		// this is a new blank volume by definition
		// create the filesystem actually
		cr, err := apiclient.NewFilesystemCreateRequest(v.FilesystemName, v.filesystemGroupName, capacity)
		if err != nil {
			return status.Errorf(codes.Internal, "Failed to create filesystem %s: %s", v.FilesystemName, err.Error())
		}
		fsObj := &apiclient.FileSystem{}
		if err := v.apiClient.CreateFileSystem(ctx, cr, fsObj); err != nil {
			return status.Error(codes.Internal, err.Error())
		}
		// create the seed snapshot
		if err := v.ensureSeedSnapshot(ctx); err != nil {
			logger.Error().Err(err).Msg("Failed to create seed snapshot, new snapshot volumes cannot be created from this filesystem!")
		}
	} else if v.isOnSnapshot() { // running on real CSI system and not in docker sanity
		// this might be either blank or copy content volume
		snapSrcUid, err := v.getUidOfSourceSnap(ctx)
		if err != nil {
			return err
		}
		fsObj, err := v.getFilesystemObj(ctx)
		if err != nil {
			return err
		}

		// create the snapshot actually
		sr, err := apiclient.NewSnapshotCreateRequest(v.SnapshotName, v.SnapshotAccessPoint, fsObj.Uid, snapSrcUid, true)
		if err != nil {
			return status.Errorf(codes.Internal, "Failed to create snapshot %s: %s", v.SnapshotName, err.Error())
		}
		snapObj := &apiclient.Snapshot{}
		if err := v.apiClient.CreateSnapshot(ctx, sr, snapObj); err != nil {
			return status.Error(codes.Internal, err.Error())
		}

		// here comes a workaround to enable running CSI sanity in detached mode
		if v.mounter.debugPath != "" {
			const xattrMount = true // no need to have xattr mount to do that
			err, unmount := v.opportunisticMount(ctx, xattrMount)
			defer unmount()
			if err != nil {
				return err
			}
			volPath := v.getFullPath(ctx, xattrMount)
			dirPath := filepath.Dir(volPath)

			// make sure that root directory is created with Default Permissions no matter what the requested permissions are
			if err := os.MkdirAll(dirPath, DefaultVolumePermissions); err != nil {
				logger.Error().Err(err).Str("directory_path", dirPath).Msg("Failed to create CSI volumes directory")
				return err
			}
			// make sure we don't hit umask upon creating directory
			oldMask := syscall.Umask(0)
			defer syscall.Umask(oldMask)

			if err := os.Mkdir(volPath, DefaultVolumePermissions); err != nil {
				logger.Error().Err(err).Str("volume_path", volPath).Msg("Failed to create volume directory")
				return err
			}
			logger.Debug().Str("full_path", v.getFullPath(ctx, true)).Msg("Successully created directory")

		}

	} else if v.hasInnerPath() { // the last condition is needed for being able to run CSI sanity in docker

		// if it was a snapshot and had inner path, it anyway should already exist.
		// So creating inner path only in such case
		const xattrMount = true // no need to have xattr mount to do that
		err, unmount := v.opportunisticMount(ctx, xattrMount)
		defer unmount()
		if err != nil {
			return err
		}
		volPath := v.getFullPath(ctx, xattrMount)

		logger.Trace().Str("inner_path", v.innerPath).Str("full_path", volPath).Interface("permissions", v.permissions).Msg("Creating directory and setting permissions")
		dirPath := filepath.Dir(volPath)

		// make sure that root directory is created with Default Permissions no matter what the requested permissions are
		if err := os.MkdirAll(dirPath, DefaultVolumePermissions); err != nil {
			logger.Error().Err(err).Str("directory_path", dirPath).Msg("Failed to create CSI volumes directory")
			return err
		}
		// make sure we don't hit umask upon creating directory
		oldMask := syscall.Umask(0)
		defer syscall.Umask(oldMask)

		if err := os.Mkdir(volPath, DefaultVolumePermissions); err != nil {
			logger.Error().Err(err).Str("volume_path", volPath).Msg("Failed to create volume directory")
			return err
		}
		logger.Debug().Msg("Successully created directory")
	}

	// Update volume capacity
	if err := v.UpdateCapacity(ctx, &(v.enforceCapacity), capacity); err != nil {
		logger.Error().Err(err).Msg("Failed to update capacity on newly created volume, reverting volume creation")
		err2 := v.Delete(ctx)
		if err2 != nil {
			logger.Warn().Err(err2).Str("inner_path", v.innerPath).Msg("Failed to clean up directory")
		}
		return err
	}

	// Update volume parameters
	if err := v.UpdateParams(ctx); err != nil {
		defer func() {
			err := v.Delete(ctx)
			if err != nil {
				logger.Error().Err(err).Str("filesystem", v.FilesystemName).Msg("Failed to delete filesystem")
			}
		}()
		return err
	}
	logger.Info().Str("filesystem", v.FilesystemName).Msg("Created volume successfully")
	return nil
}

func (v *UnifiedVolume) getUidOfSourceSnap(ctx context.Context) (*uuid.UUID, error) {
	logger := log.Ctx(ctx)
	var srcSnap *apiclient.Snapshot
	var err error
	if v.srcSnapshot != nil {
		logger.Trace().Msg("Attempting to fetch the Weka snapshot of CSI source snap")
		srcSnap, err = v.srcSnapshot.getObject(ctx)
	} else if v.srcVolume != nil && v.srcVolume.isOnSnapshot() {
		logger.Trace().Msg("Attempting to fetch the Weka snapshot of CSI source volume")
		srcSnap, err = v.srcVolume.getSnapshotObj(ctx)
	} else if v.hasSeedSnapshot(ctx) {
		logger.Trace().Msg("Attempting to fetch the Weka seed snapshot filesystem")
		srcSnap, err = v.getSeedSnapshot(ctx)
	}
	if err != nil {
		logger.Error().Err(err).Msg("Failed to locate source snapshot object")
		return nil, status.Error(codes.Internal, err.Error())
	}
	if srcSnap == nil || srcSnap.Uid == uuid.Nil {
		logger.Trace().Msg("There is no Weka source snapshot to originate from")
		return nil, nil
	}
	return &(srcSnap.Uid), nil
}

// Delete is a synchronous delete, used for cleanup on unsuccessful ControllerCreateVolume. GC flow is separate
func (v *UnifiedVolume) Delete(ctx context.Context) error {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	var err error
	logger.Debug().Msg("Starting deletion of volume")
	if v.isFilesystem() {
		v.deleteSeedSnapshot(ctx)
		if !v.isAllowedForDeletion(ctx) {
			return status.Errorf(codes.FailedPrecondition, "volume cannot be deleted since it has underlying snapshots")
		}
		err = v.deleteFilesystem(ctx)
	} else if v.isOnSnapshot() {
		err = v.deleteSnapshot(ctx)
	} else {
		err = v.deleteDirectory(ctx)
	}
	if err != nil {
		logger.Error().Err(err).Msg("Failed to delete volume")
	}
	logger.Debug().Msg("Deletion of volume completed successfully")
	return err
}

func (v *UnifiedVolume) deleteDirectory(ctx context.Context) error {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	const xattrMount = false // no need to have xattr mount to do that
	err, unmount := v.opportunisticMount(ctx, xattrMount)
	defer unmount()
	if err != nil {
		return err
	}

	logger.Trace().Msg("Deleting volume")
	volPath := v.getFullPath(ctx, xattrMount)
	_ = os.RemoveAll(volPath)
	logger.Trace().Str("full_path", volPath).Msg("Deleted contents of volume")
	return nil
}

func (v *UnifiedVolume) deleteFilesystem(ctx context.Context) error {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	fsObj, err := v.getFilesystemObj(ctx)
	if err != nil {
		return status.Errorf(codes.Internal, "Failed to delete filesystem %s", v.FilesystemName)
	}
	if fsObj == nil || fsObj.Uid == uuid.Nil {
		logger.Warn().Str("filesystem", v.FilesystemName).Msg("Apparently filesystem not exists, returning OK")
		// FS doesn't exist already, return OK for idempotence
		return nil
	}
	fsUid := fsObj.Uid
	logger.Trace().Str("filesystem", v.FilesystemName).Msg("Attempting deletion of filesystem")
	fsd := &apiclient.FileSystemDeleteRequest{Uid: fsObj.Uid}
	err = v.apiClient.DeleteFileSystem(ctx, fsd)
	if err != nil {
		if err == apiclient.ObjectNotFoundError {
			logger.Debug().Str("filesystem", v.FilesystemName).Msg("Filesystem not found, assuming repeating request")
			return nil
		}
		logger.Error().Err(err).Str("filesystem", v.FilesystemName).Msg("Failed to delete filesystem")
		return status.Errorf(codes.Internal, "Failed to delete filesystem %s: %s", v.FilesystemName, err)
	}
	logger.Trace().Msg("Waiting for filesystem deletion to complete")
	for start := time.Now(); time.Since(start) < MaxSnapshotDeletionDuration; {
		fsObj := &apiclient.FileSystem{}
		err := v.apiClient.GetFileSystemByUid(ctx, fsUid, fsObj)
		if err != nil {
			if err == apiclient.ObjectNotFoundError {
				logger.Trace().Str("filesystem", v.FilesystemName).Msg("Filesystem was removed successfully")
				return nil
			}
			return err
		}
		if fsObj.Uid != uuid.Nil {
			if fsObj.IsRemoving {
				logger.Trace().Str("filesystem", v.FilesystemName).Msg("Filesystem is still being removed")
			} else {
				return errors.New(fmt.Sprintf("FilesystemName %s not marked for deletion but it should", v.FilesystemName))
			}
		}
		time.Sleep(time.Second)
	}

	logger.Error().Str("filesystem", v.FilesystemName).Msg("Timeout deleting volume")
	return nil
}

func (v *UnifiedVolume) deleteSnapshot(ctx context.Context) error {
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()
	snapObj, err := v.getSnapshotObj(ctx)
	if err != nil {
		logger.Error().Err(err).Str("snapshot", v.SnapshotName).Msg("Failed to delete snapshot")
		return status.Errorf(codes.Internal, "Failed to delete snapshot %s", v.SnapshotName)
	}
	if snapObj == nil || snapObj.Uid == uuid.Nil {
		logger.Debug().Str("snapshot", v.SnapshotName).Msg("Snapshot not found, assuming repeating request")
		// FS doesn't exist already, return OK for idempotence
		return nil
	}
	snapUid := snapObj.Uid
	logger.Trace().Str("snapshot_uid", snapUid.String()).Msg("Attempting deletion of snapshot")
	fsd := &apiclient.SnapshotDeleteRequest{Uid: snapObj.Uid}
	err = v.apiClient.DeleteSnapshot(ctx, fsd)
	if err != nil {
		if err == apiclient.ObjectNotFoundError {
			logger.Debug().Str("snapshot", v.SnapshotName).Msg("Snapshot not found, assuming repeating request")
			return nil
		}
		logger.Error().Err(err).Str("snapshot", v.SnapshotName).Str("snapshot_uid", snapUid.String()).
			Msg("Failed to delete snapshot")
		return status.Errorf(codes.Internal, "Failed to delete filesystem %s: %s", v.FilesystemName, err)
	}
	logger.Trace().Msg("Waiting for snapshot deletion to complete")
	for start := time.Now(); time.Since(start) < MaxSnapshotDeletionDuration; {
		snapObj := &apiclient.Snapshot{}
		err := v.apiClient.GetSnapshotByUid(ctx, snapUid, snapObj)
		if err != nil {
			if err == apiclient.ObjectNotFoundError {
				logger.Trace().Msg("Snapshot was removed successfully")
				return nil
			}
			return err
		}
		if snapObj.Uid != uuid.Nil {
			if snapObj.IsRemoving {
				logger.Trace().Msg("Snapshot is still being removed")
			} else {
				return errors.New(fmt.Sprintf("Snapshot %s not marked for deletion but it should", v.SnapshotUuid.String()))
			}
		}
		time.Sleep(time.Second)
	}

	logger.Info().Str("filesystem", v.FilesystemName).Str("snapshot", v.SnapshotName).Msg("Volume deleted successfully")
	return nil
}

// SetParamsFromRequestParams takes additional optional params from storage class params and applies them to Volume object
// those params then need to be set during actual volume creation via UpdateParams function
func (v *UnifiedVolume) SetParamsFromRequestParams(ctx context.Context, params map[string]string) error {
	// filesystem group name, required for actually creating a raw FS
	if val, ok := params["filesystemGroupName"]; ok {
		v.filesystemGroupName = val
		if v.filesystemGroupName == "" {
			return status.Error(codes.InvalidArgument, "FilesystemGroupName not specified")
		}
	}

	// permissions
	if val, ok := params["permissions"]; ok {
		raw, err := strconv.ParseInt(val, 0, 32)
		if err != nil {
			return err
		}
		v.permissions = fs.FileMode(uint32(raw))
	}
	// ownership
	if val, ok := params["ownerUid"]; ok {
		raw, err := strconv.Atoi(val)
		if err != nil {
			return err
		}
		v.ownerUid = raw
	}
	if val, ok := params["ownerGid"]; ok {
		raw, err := strconv.Atoi(val)
		if err != nil {
			return err
		}
		v.ownerGid = raw
	}

	// capacityEnforcement
	enforceCapacity, err := getCapacityEnforcementParam(params)
	if err != nil {
		return err
	}
	v.enforceCapacity = enforceCapacity
	return nil
}

// CreateSnapshot creates a UnifiedSnapshot object which repres(this is not yet the CSI snapshot)
// The snapshot object will have a method to convert it to Csi snapshot object
func (v *UnifiedVolume) CreateSnapshot(ctx context.Context, name string) (Snapshot, error) {
	s, err := NewSnapshotFromVolumeCreate(ctx, name, v, v.apiClient, v.server)
	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Str("snapshot_id", s.GetId()).Logger()
	if err != nil {
		return &UnifiedSnapshot{}, err
	}
	// check if snapshot with this name already exists
	exists, err := s.Exists(ctx)
	if err != nil {
		return &UnifiedSnapshot{}, err
	}
	if exists {
		logger.Trace().Msg("Seems that snapshot already exists")
		return s, err
	}

	logger.Debug().Msg("Attempting to create snapshot")
	if err := s.Create(ctx); err != nil {
		return s, err
	}
	logger.Info().Msg("Snapshot created successfully")
	return s, nil
}

// canBeOperated returns true if the object can be CRUDed without API backing (basically only dirVolume without snapshot)
func (v *UnifiedVolume) canBeOperated() error {
	if v.SnapshotUuid != nil {
		if v.apiClient == nil && v.mounter.debugPath == "" {
			return errors.New("Cannot operate volume of this type without API binding")
		}

		if !v.apiClient.SupportsFilesystemAsVolume() {
			return errors.New("volume of type Filesystem is not supported on current version of Weka cluster")
		}
	}
	return nil
}

func (v *UnifiedVolume) isMounted(ctx context.Context, xattr bool) bool {
	path := v.mountPath[xattr]
	if path != "" && PathIsWekaMount(ctx, path) {
		return true
	}
	return false
}

func (v *UnifiedVolume) GetMountPoint(ctx context.Context, xattr bool) (string, error) {
	if !v.isMounted(ctx, xattr) {
		return "", errors.New(fmt.Sprintf("volume %s is not mounted", v.GetId()))
	}
	return v.mountPath[xattr], nil
}

func (v *UnifiedVolume) EnsureRightCapacity(ctx context.Context, expectedCapacity int64) (bool, error) {
	currentCapacity, err := v.GetCapacity(ctx)
	if err != nil {
		return false, err
	}
	if currentCapacity == 0 {
		return false, status.Error(codes.Internal, "Could not a valid current capacity of the volume")
	}

	return currentCapacity == expectedCapacity, nil
}
