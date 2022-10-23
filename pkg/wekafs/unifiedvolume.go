package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/golang/glog"
	"github.com/google/uuid"
	"github.com/pkg/xattr"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const (
	MaxFileSystemDeletionDuration = time.Hour * 2 // max time for FS deletion

	AllowAutoExpandFilesystemForSnapVols      = true // whether we allow resize of filesystem to expand Snap vol
	AllowAutoExpandFilesystemForInnerPathVols = true // whether we allow resize of filesystem to expand innerPath vol
	MaxHashLengthForObjectNames               = 12
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
	mounter             *wekaMounter
	mountPath           map[bool]string
	ssdCapacityPercent  int
	enforceCapacity     bool

	srcSnapshotUid *uuid.UUID
}

func (v *UnifiedVolume) String() string {
	ret := string(v.GetType()) + "/" + v.FilesystemName
	if v.SnapshotUuid != nil {
		ret = ret + ":" + v.SnapshotName
	}
	if v.hasInnerPath() {
		ret = ret + "/" + v.innerPath
	}
	return ret
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
	if v.isOnSnapshot() {
		return false, nil
	}
	snapshots, err := v.getUnderlyingSnapshots(ctx)
	if err != nil {
		return true, err
	}
	return snapshots != nil, nil
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

// UpdateParams updates params on volume upon creation. Was part of Create initially, but must be done after content source is applied
func (v *UnifiedVolume) UpdateParams(ctx context.Context) error {

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
		glog.Errorf("Failed to change directory %s", volPath)
		return err
	}

	// Update volume UID/GID if set in storage class
	if v.ownerUid+v.ownerGid != 0 {
		glog.Infof("Setting volume ownership to %d:%d", v.ownerUid, v.ownerGid)
		if err := os.Chown(volPath, v.ownerUid, v.ownerGid); err != nil {
			return err
		}
	}
	return nil
}

// getFilesystemFreeSpace returns maximum capacity that can be obtained on filesystem, e.g. disk free (for directory volumes)
func (v *UnifiedVolume) getFilesystemFreeSpace(ctx context.Context) (int64, error) {
	const xattrMount = true // need to have xattr mount to do that
	err, unmount := v.opportunisticMount(ctx, xattrMount)
	defer unmount()
	if err != nil {
		return 0, err
	}

	glog.V(5).Infoln("Attempting to get max capacity available on root FS", v.FilesystemName)

	var stat syscall.Statfs_t
	mountPath := v.mountPath[xattrMount]
	err = syscall.Statfs(mountPath, &stat)
	if err != nil {
		return -1, status.Errorf(codes.FailedPrecondition, "Could not obtain free capacity on mount path %s: %s", mountPath, err.Error())
	}
	// Available blocks * size per block = available space in bytes
	maxCapacity := int64(stat.Bavail * uint64(stat.Bsize))
	glog.V(4).Infoln("Max capacity available for a volume:", maxCapacity)
	return maxCapacity, nil
}

// getFreeSpaceOnStorage returns maximum capacity on storage (for either creating new or resizing an FS)
func (v *UnifiedVolume) getFreeSpaceOnStorage(ctx context.Context) (int64, error) {
	fsStr := "NEW"
	existsOk, err := v.Exists(ctx)
	if err != nil {
		return -1, status.Errorf(codes.Internal, "Failed to check for existence of volume")
	}
	if existsOk {
		fsStr = "EXISTING"
	}

	glog.V(5).Infoln("Attempting to get max capacity for a", fsStr, "filesystem", v.FilesystemName)
	if v.apiClient == nil {
		return -1, status.Errorf(codes.FailedPrecondition, "Could not bind volume %s to API endpoint", v.FilesystemName)
	}
	maxCapacity, err := v.apiClient.GetFreeCapacity(ctx)
	if err != nil {
		return -1, status.Errorf(codes.FailedPrecondition, "Could not obtain free capacity for filesystem %s on cluster %s: %s", v.GetId(), v.apiClient.ClusterName, err.Error())
	}
	glog.V(5).Infoln("Resolved free capacity as", maxCapacity)
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
	const xattrMount = true // need to have xattr mount to do that
	err, unmount := v.opportunisticMount(ctx, xattrMount)
	defer unmount()
	if err != nil {
		return 0, err
	}

	glog.V(3).Infoln("Attempting to get current capacity of volume", v.GetId())
	if v.apiClient != nil && v.apiClient.SupportsQuotaDirectoryAsVolume() {
		size, err := v.getSizeFromQuota(ctx)
		if err == nil {
			glog.V(3).Infoln("Current capacity of volume", v.GetId(), "is", size, "obtained via API")
			return int64(size), nil
		}
	}
	glog.V(4).Infoln("Volume", v.GetId(), "appears to be a legacy volume. Trying to fetch capacity from Xattr")
	size, err := v.getSizeFromXattr(ctx)
	if err != nil {
		return 0, err
	}
	glog.V(3).Infoln("Current capacity of volume", v.GetId(), "is", size, "obtained via Xattr")
	return int64(size), nil
}

func (v *UnifiedVolume) getCapacityFromFsSize(ctx context.Context) (int64, error) {
	glog.V(3).Infoln("Attempting to get current capacity of volume", v.GetId())
	fsObj, err := v.getFilesystemObj(ctx)
	if err != nil {
		return -1, err
	}
	size := fsObj.TotalCapacity
	if size > 0 {
		glog.V(3).Infoln("Current capacity of volume", v.GetId(), "is", size, "obtained via API")
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

func (v *UnifiedVolume) calcRequiredSsdCapacity(requiredCapacity int64) *int64 {
	if v.ssdCapacityPercent == 100 {
		return nil
	}
	ret := requiredCapacity / 100 * int64(v.ssdCapacityPercent/100)
	return &ret
}

func (v *UnifiedVolume) resizeFilesystem(ctx context.Context, capacity int64) error {
	fsObj, err := v.getFilesystemObj(ctx)
	if err != nil {
		return err
	}

	capLimit := capacity
	ssdLimit := v.calcRequiredSsdCapacity(capLimit)

	fsu := apiclient.NewFileSystemResizeRequest(fsObj.Uid, &capLimit, ssdLimit)
	err = v.apiClient.UpdateFileSystem(ctx, fsu, fsObj)
	return err
}

func (v *UnifiedVolume) ensureSufficientFsSizeOnUpdateCapacity(ctx context.Context, capacityLimit int64) error {
	// check if we need to resize filesystem actually for snapshot volume as otherwise user might hit limits regardless of quota
	// this is important for all types of volumes (FS, FSSNAP, Dir)
	// NOTE1: but for DirVolume we still can't ensure user will not hit limits due to sharing single FS / SNAP between multiple DirVolumes
	// NOTE2: this cannot be done without API
	if v.apiClient == nil {
		glog.V(4).Infoln("Volume is not bound to API client. Cannot validate filesystem size")
		return nil
	} else {

	}
	currentFsCapacity, err := v.getCapacityFromFsSize(ctx)
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, "Failed to get current volume capacity for volume %s", v.GetId())
	}
	if currentFsCapacity < capacityLimit {
		// TODO: replace it with cs.allowAutoExpandFs...
		if (v.isOnSnapshot() && !AllowAutoExpandFilesystemForSnapVols) || (v.hasInnerPath() && !AllowAutoExpandFilesystemForInnerPathVols) {
			// do not permit auto-resize of filesystems
			return status.Errorf(codes.FailedPrecondition, "Not allowed to expand volume of %s as underlying filesystem %s is too small", v.GetType(), v.FilesystemName)
		}
		glog.V(3).Infoln("New volume size doesn't fit current filesystem limits, expanding filesystem", v.FilesystemName, "to", capacityLimit)
		// TODO: Add actual FS resize and err handling
	}
	return nil
}

func (v *UnifiedVolume) UpdateCapacity(ctx context.Context, enforceCapacity *bool, capacityLimit int64) error {
	// check if required AND possible to expand filesystem, expand if needed or fail
	if err := v.ensureSufficientFsSizeOnUpdateCapacity(ctx, capacityLimit); err != nil {
		return err
	}

	// update capacity of the volume by updating quota object on its root directory (or XATTR)
	glog.V(3).Infoln("Updating capacity of the volume", v.GetId(), "to", capacityLimit)
	var fallback = true // whether we should try to use xAttr fallback or not
	f := func() error { return v.updateCapacityQuota(ctx, enforceCapacity, capacityLimit) }
	if v.apiClient == nil {
		glog.V(4).Infof("Volume has no API client bound, updating capacity in legacy mode")
		f = func() error { return v.updateCapacityXattr(ctx, enforceCapacity, capacityLimit) }
		fallback = false
	} else if !v.apiClient.SupportsQuotaDirectoryAsVolume() {
		glog.V(4).Infoln("Updating quota via API not supported by Weka cluster, updating capacity in legacy mode")
		f = func() error { return v.updateCapacityXattr(ctx, enforceCapacity, capacityLimit) }
		fallback = false
	} else if !v.apiClient.SupportsAuthenticatedMounts() && v.apiClient.Credentials.Organization != "Root" {
		glog.V(4).Infoln("Updating quota via API is not supported by Weka cluster since filesystem is located in non-default organization, updating capacity in legacy mode")
		f = func() error { return v.updateCapacityXattr(ctx, enforceCapacity, capacityLimit) }
		fallback = false
	}
	err := f()
	if err == nil {
		glog.V(3).Infoln("Successfully updated capacity for volume", v.GetId())
		return err
	}
	if fallback {
		glog.V(4).Infof("Failed to set quota for a volume (%s), falling back to xattr", err.Error())
		f = func() error { return v.updateCapacityXattr(ctx, enforceCapacity, capacityLimit) }
		err := f()
		if err == nil {
			glog.V(3).Infoln("Successfully updated capacity for volume in FALLBACK mode", v.GetId())
		} else {
			glog.Errorln("Failed to set capacity for quota volume", v.GetId(), "even in fallback")
		}
		return err
	}
	return err
}

func (v *UnifiedVolume) updateCapacityQuota(ctx context.Context, enforceCapacity *bool, capacityLimit int64) error {
	if enforceCapacity != nil {
		glog.V(4).Infoln("Updating quota on volume", v.GetId(), "to", capacityLimit, "enforceCapacity:", *enforceCapacity)
	} else {
		glog.V(4).Infoln("Updating quota on volume", v.GetId(), "to", capacityLimit, "enforceCapacity:", "RETAIN")
	}
	inodeId, err := v.getInodeId(ctx)
	if err != nil {
		glog.Errorln("Failed to fetch inode ID for volume", v.GetId())
		return err
	}
	fsObj, err := v.getFilesystemObj(ctx)
	if err != nil {
		glog.Errorln("Failed to fetch filesystem for volume", v.GetId(), err)
		return err
	}

	// check if the quota already exists. If not - create it and exit
	q, err := v.apiClient.GetQuotaByFileSystemAndInode(ctx, fsObj, inodeId)
	if err != nil {
		if err != apiclient.ObjectNotFoundError {
			// any other error
			glog.Errorln("Failed to get quota:", err)
			return status.Error(codes.Internal, err.Error())
		}
		_, err := v.setQuota(ctx, enforceCapacity, uint64(capacityLimit))
		return err
	}

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

	if q.GetQuotaType() == quotaType && q.GetCapacityLimit() == uint64(capacityLimit) {
		glog.V(4).Infoln("No need to update quota as it matches current")
		return nil
	}
	updatedQuota := &apiclient.Quota{}
	r := apiclient.NewQuotaUpdateRequest(*fsObj, inodeId, quotaType, uint64(capacityLimit))
	glog.V(5).Infoln("Constructed update request", r.String())
	err = v.apiClient.UpdateQuota(ctx, r, updatedQuota)
	if err != nil {
		glog.V(4).Infoln("Failed to set quota on volume", v.GetId(), "to", updatedQuota.GetQuotaType(), updatedQuota.GetCapacityLimit(), err)
		return err
	}
	glog.V(4).Infoln("Successfully set quota on volume", v.GetId(), "to", updatedQuota.GetQuotaType(), updatedQuota.GetCapacityLimit())
	return nil
}

func (v *UnifiedVolume) updateCapacityXattr(ctx context.Context, enforceCapacity *bool, capacityLimit int64) error {
	const xattrMount = true // must have xattrs for this case
	if !v.isMounted(xattrMount) {
		err, unmountFunc := v.Mount(ctx, xattrMount)
		if err != nil {
			return err
		} else {
			defer unmountFunc()
		}
	}

	glog.V(4).Infoln("Updating xattrs on volume", v.GetId(), "to", capacityLimit)
	if enforceCapacity != nil && *enforceCapacity {
		glog.V(3).Infof("Legacy volume does not support enforce capacity")
	}
	err := setVolumeProperties(v.getFullPath(ctx, xattrMount), capacityLimit, v.innerPath)
	if err != nil {
		glog.Errorln("Failed to update xattrs on volume", v.GetId(), "capacity is not set")
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
			glog.Errorln("Failed to delete filesystem", err)
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
	const xattrMount = false // no need to have xattr mount to do that
	err, unmount := v.opportunisticMount(ctx, xattrMount)
	defer unmount()
	if err != nil {
		return 0, err
	}

	fullPath := v.getFullPath(ctx, xattrMount)
	glog.V(5).Infoln("Getting inode ID of volume", v.GetId(), "fullPath: ", fullPath)
	fileInfo, err := os.Stat(fullPath)
	if err != nil {
		glog.Error(err)
		return 0, err
	}
	stat, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New(fmt.Sprintf("failed to obtain inodeId from %s", v.mountPath[xattrMount]))
	}
	glog.V(5).Infoln("Inode ID of the volume", v.GetId(), "is", stat.Ino)
	return stat.Ino, nil
}

// GetId returns the ID of the volume
func (v *UnifiedVolume) GetId() string {
	return v.id
}

// setQuota creates a quota object for the volume. set for every type of volume including root of FS
func (v *UnifiedVolume) setQuota(ctx context.Context, enforceCapacity *bool, capacityLimit uint64) (*apiclient.Quota, error) {
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
	glog.V(4).Infoln("Creating a quota for volume", v.GetId(), "capacity limit:", capacityLimit, "quota type:", quotaType)

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
	glog.V(4).Infoln("Quota successfully set for volume", v.GetId())
	return q, nil
}

// getQuota returns quota object for volume (if exists) or error
func (v *UnifiedVolume) getQuota(ctx context.Context) (*apiclient.Quota, error) {
	glog.V(4).Infoln("Getting existing quota for volume", v.GetId())
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
		glog.V(4).Infoln("Successfully acquired existing quota for volume", v.GetId(), ret.GetQuotaType(), ret.GetCapacityLimit())
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
	if !v.isMounted(xattr) {
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
	if (v.isOnSnapshot() || v.isFilesystem()) && v.apiClient == nil {
		glog.Infof("No API bound, assuming volume %s does not exist", v.String())
		return false, nil
	}
	if v.isFilesystem() {
		return v.fileSystemExists(ctx)
	}
	if v.isOnSnapshot() {
		exists, err := v.snapshotExists(ctx)
		if err != nil {
			glog.Errorln("Failed to fetch if snapshot exists:", v.String())
			return false, err
		}
		if !exists {
			glog.V(3).Infoln("Snapshot representing the volume does not exist on storage")
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
			glog.Infof("Volume %s not found on filesystem %s", v.GetId(), v.FilesystemName)
			return false, nil
		}
		if err := pathIsDirectory(v.getFullPath(ctx, xattrMount)); err != nil {
			glog.Errorf("Volume %s is unusable: path %s is a not a directory", v.GetId(), v.FilesystemName)
			return false, status.Error(codes.Internal, err.Error())
		}
		glog.Infof("Volume %s exists and accessible via %s", v.GetId(), v.getFullPath(ctx, xattrMount))
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
	glog.V(3).Infoln("Checking if volume", v.GetId(), "exists")
	fsObj, err := v.getFilesystemObj(ctx)
	if err != nil {
		glog.V(3).Infoln("Failed to fetch volume", v.GetId(), "from underlying storage", err.Error())
		return false, err
	}
	if fsObj == nil || fsObj.Uid == uuid.Nil {
		glog.V(3).Infoln("Volume", v.GetId(), "does not exist")
		return false, err
	}
	if fsObj.IsRemoving {
		glog.V(3).Infoln("Volume", v.GetId(), "exists but is in removing state")
		return false, nil
	}
	glog.Infof("Volume %s exists", v.GetId())
	return true, nil
}

func (v *UnifiedVolume) snapshotExists(ctx context.Context) (bool, error) {
	glog.V(3).Infoln("Checking if volume", v.GetId(), "exists")
	snapObj, err := v.getSnapshotObj(ctx)
	if err != nil {
		glog.V(3).Infoln("Failed to fetch volume", v.GetId(), "from underlying storage", err.Error())
		return false, err
	}
	if snapObj == nil || snapObj.Uid == uuid.Nil {
		glog.V(3).Infoln("Volume", v.GetId(), "does not exist")
		return false, err
	}
	if snapObj.IsRemoving {
		glog.V(3).Infoln("Volume", v.GetId(), "exists but is in removing state")
		return false, nil
	}
	glog.Infof("Volume %s exists", v.GetId())
	return true, nil
}

// Create actually creates the storage location for the particular volume object
func (v *UnifiedVolume) Create(ctx context.Context, capacity int64) error {

	// validate minimum capacity before create new volume
	glog.V(3).Infoln("Validating enough capacity on storage for creating the volume")
	maxStorageCapacity, err := v.getMaxCapacity(ctx)
	if err != nil {
		return status.Errorf(codes.Internal, fmt.Sprintf("CreateVolume: Cannot obtain free capacity for volume %s", v.GetId()))
	}
	if capacity > maxStorageCapacity {
		return status.Errorf(codes.OutOfRange, fmt.Sprintf("Requested capacity %d exceeds maximum allowed %d", capacity, maxStorageCapacity))
	}
	if v.isFilesystem() {
		// create the filesystem actually
		cr, err := apiclient.NewFilesystemCreateRequest(v.FilesystemName, v.filesystemGroupName, capacity)
		glog.Infoln(cr)
		if err != nil {
			return status.Errorf(codes.Internal, "Failed to create filesystem %s: %s", v.FilesystemName, err.Error())
		}
		fsObj := &apiclient.FileSystem{}
		if err := v.apiClient.CreateFileSystem(ctx, cr, fsObj); err != nil {
			return status.Error(codes.Internal, err.Error())
		}

	}

	// TODO: move all to params
	const xattrMount = true // no need to have xattr mount to do that
	err, unmount := v.opportunisticMount(ctx, xattrMount)
	defer unmount()
	if err != nil {
		return err
	}

	volPath := v.getFullPath(ctx, xattrMount)
	if v.hasInnerPath() { // ensure that we have the root directory on which we will later create the volume
		glog.Infof("Creating directory %s with permissions %s", volPath, v.permissions)
		dirPath := filepath.Dir(volPath)

		// make sure that root directory is created with Default Permissions no matter what the requested permissions are
		if err := os.MkdirAll(dirPath, DefaultVolumePermissions); err != nil {
			glog.Errorf("Failed to create CSI volumes directory %s", dirPath)
			return err
		}
		// make sure we don't hit umask upon creating directory
		oldMask := syscall.Umask(0)
		defer syscall.Umask(oldMask)

		if err := os.Mkdir(volPath, DefaultVolumePermissions); err != nil {
			glog.Errorf("Failed to create directory %s", volPath)
			return err
		}
		glog.Infof("Created directory %s", v.getFullPath(ctx, xattrMount))
	}

	glog.Infof("Updating capacity of %s to %v", v.getFullPath(ctx, xattrMount), capacity)
	// Update volume capacity
	if err := v.UpdateCapacity(ctx, &(v.enforceCapacity), capacity); err != nil {
		glog.Warningf("Failed to update capacity on newly created volume %s in: %s, deleting", v.GetId(), volPath)
		err2 := v.Delete(ctx)
		if err2 != nil {
			glog.V(2).Infof("Failed to clean up directory %s after unsuccessful set capacity", v.innerPath)
		}
		return err
	}

	// Update volume parameters
	if err := v.UpdateParams(ctx); err != nil {
		defer func() {
			err := v.Delete(ctx)
			if err != nil {
				glog.Errorf("Failed to delete filesystem %s: %s", v.GetId(), err.Error())
			}
		}()
		return err
	}
	glog.V(3).Infof("Created volume %s, mapped to filesystem %v", v.GetId(), v.FilesystemName)
	return nil
}

// Delete is a synchronous delete, used for cleanup on unsuccessful ControllerCreateVolume. GC flow is separate
func (v *UnifiedVolume) Delete(ctx context.Context) error {
	var err error
	glog.V(3).Infoln("Starting deletion of volume", v.String())
	defer glog.V(3).Infoln("Deletion of volume completed:", v.String())
	if v.isFilesystem() {
		if !v.isAllowedForDeletion(ctx) {
			time.Sleep(time.Minute)
			return status.Errorf(codes.FailedPrecondition, "volume cannot be deleted since it has underlying snapshots")
		}
		err = v.deleteFilesystem(ctx)
	} else if v.isOnSnapshot() {
		err = v.deleteSnapshot(ctx)
	} else {
		err = v.deleteDirectory(ctx)
	}
	if err != nil {
		glog.Errorf("Failed to delete volume %s: %s", v.String(), err.Error())
	}
	return err
}

func (v *UnifiedVolume) deleteDirectory(ctx context.Context) error {
	const xattrMount = false // no need to have xattr mount to do that
	err, unmount := v.opportunisticMount(ctx, xattrMount)
	defer unmount()
	if err != nil {
		return err
	}

	glog.V(4).Infof("Deleting volume %s", v.String())
	volPath := v.getFullPath(ctx, xattrMount)
	_ = os.RemoveAll(volPath)
	glog.V(2).Infof("Deleted contents of volume %s in %v", v.GetId(), volPath)
	return nil
}

func (v *UnifiedVolume) deleteFilesystem(ctx context.Context) error {
	fsObj, err := v.getFilesystemObj(ctx)
	if err != nil {
		glog.Errorln("Failed to delete filesystem since FS object could not be fetched from API for filesystem", v.FilesystemName)
		return status.Errorf(codes.Internal, "Failed to delete filesystem %s", v.FilesystemName)
	}
	if fsObj == nil || fsObj.Uid == uuid.Nil {
		glog.Errorln("Apparently filesystem not exists, returning OK", v.FilesystemName)
		// FS doesn't exist already, return OK for idempotence
		return nil
	}
	fsUid := fsObj.Uid
	glog.Infoln("Attempting deletion of filesystem", v.FilesystemName)
	fsd := &apiclient.FileSystemDeleteRequest{Uid: fsObj.Uid}
	err = v.apiClient.DeleteFileSystem(ctx, fsd)
	if err != nil {
		if err == apiclient.ObjectNotFoundError {
			glog.Errorf("FilesystemName %s not found, assuming repeating delete request", v.FilesystemName)
			return nil
		}
		glog.Errorln("Failed to delete filesystem via API", v.FilesystemName, err)
		return status.Errorf(codes.Internal, "Failed to delete filesystem %s: %s", v.FilesystemName, err)
	}
	glog.V(6).Infoln("Polling filesystem to ensure it is deleted")
	for start := time.Now(); time.Since(start) < time.Second*30; {
		fsObj := &apiclient.FileSystem{}
		err := v.apiClient.GetFileSystemByUid(ctx, fsUid, fsObj)
		if err != nil {
			if err == apiclient.ObjectNotFoundError {
				glog.Info("Volume was removed successfully")
				return nil
			}
			return err
		}
		if fsObj.Uid != uuid.Nil {
			if fsObj.IsRemoving {
				glog.V(5).Infof("FilesystemName %s is still removing", v.FilesystemName)
			} else {
				return errors.New(fmt.Sprintf("FilesystemName %s not marked for deletion but it should", v.FilesystemName))
			}
		}
		time.Sleep(time.Second)
	}

	glog.V(4).Infof("Deleted volume %s, mapped to filesystem %s", v.GetId(), v.FilesystemName)
	return nil
}

func (v *UnifiedVolume) deleteSnapshot(ctx context.Context) error {
	snapObj, err := v.getSnapshotObj(ctx)
	if err != nil {
		glog.Errorln("Failed to delete volume since snapshot object could not be fetched from API for snapshot ID", v.SnapshotUuid.String())
		return status.Errorf(codes.Internal, "Failed to delete filesystem %s", v.FilesystemName)
	}
	if snapObj == nil || snapObj.Uid == uuid.Nil {
		glog.Errorln("Apparently snapshot not exists, returning OK", v.FilesystemName)
		// FS doesn't exist already, return OK for idempotence
		return nil
	}
	snapUid := snapObj.Uid
	glog.Infoln("Attempting deletion of snapshot", v.SnapshotUuid.String())
	fsd := &apiclient.SnapshotDeleteRequest{Uid: snapObj.Uid}
	err = v.apiClient.DeleteSnapshot(ctx, fsd)
	if err != nil {
		if err == apiclient.ObjectNotFoundError {
			glog.Errorf("Snapshot %s not found, assuming repeating delete request", v.SnapshotUuid.String())
			return nil
		}
		glog.Errorln("Failed to delete snapshot via API", v.SnapshotUuid.String(), err)
		return status.Errorf(codes.Internal, "Failed to delete filesystem %s: %s", v.FilesystemName, err)
	}
	glog.V(6).Infoln("Polling snapshot to ensure it is deleted")
	for start := time.Now(); time.Since(start) < MaxSnapshotDeletionDuration; {
		snapObj := &apiclient.Snapshot{}
		err := v.apiClient.GetSnapshotByUid(ctx, snapUid, snapObj)
		if err != nil {
			if err == apiclient.ObjectNotFoundError {
				glog.Info("Volume was removed successfully")
				return nil
			}
			return err
		}
		if snapObj.Uid != uuid.Nil {
			if snapObj.IsRemoving {
				glog.V(5).Infof("Snapshot %s is still removing", v.SnapshotUuid.String())
			} else {
				return errors.New(fmt.Sprintf("Snapshot %s not marked for deletion but it should", v.SnapshotUuid.String()))
			}
		}
		time.Sleep(time.Second)
	}

	glog.V(4).Infof("Deleted volume %s, mapped to filesystem %s", v.GetId(), v.FilesystemName)
	return nil
}

// SetParamsFromRequestParams takes additional optional params from storage class params and applies them to Volume object
// those params then need to be set during actual volume creation via UpdateParams function
func (v *UnifiedVolume) SetParamsFromRequestParams(params map[string]string) error {
	// filesystem group name, required for actually creating a raw FS
	if val, ok := params["filesystemGroupName"]; ok {
		v.filesystemGroupName = val
		glog.V(5).Infoln("Set filesystemGroupName:", v.filesystemGroupName)
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

	// ssdCapacityPercent
	if val, ok := params["ssdCapacityPercent"]; ok {
		ssdPercent, err := strconv.Atoi(val)
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "Failed to parse percents from storageclass")
		}
		v.ssdCapacityPercent = ssdPercent
	} else {
		// default value
		v.ssdCapacityPercent = 100
	}
	return nil
}

// CreateSnapshot creates a UnifiedSnapshot object and creates its actual Weka counterpart (this is not yet the CSI snapshot)
// The snapshot object will have a method to convert it to Csi snapshot object
func (v *UnifiedVolume) CreateSnapshot(ctx context.Context, name string) (Snapshot, error) {
	s, err := NewSnapshotFromVolumeCreate(ctx, name, v, v.apiClient)
	if err != nil {
		return &UnifiedSnapshot{}, err
	}

	// check if snapshot with this name already exists
	snapObj, err := s.getObject(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to check for existence of snapshot")
	}
	if snapObj != nil {
		glog.Infoln("Got an existing snapshot, checking for idempotence")
		// check if we are not asked to create same snapshot name from different source volume ID
		glog.Infoln("NEW:", "SnapID:", s.GetId(),
			"WekaSnapName:", s.getInternalSnapName(),
			"AccessPoint:", s.getInternalAccessPoint(),
			"paramsHash:", s.getParamsHash())
		glog.Infoln("EXISTING:", "SnapID:", s.GetId(),
			"WekaSnapName:", snapObj.Name,
			"AccessPoint:", snapObj.AccessPoint,
			"paramsHash:", calculateSnapshotParamsHash(name, v.GetId()))
		if s.getParamsHash() != calculateSnapshotParamsHash(name, v.GetId()) {
			glog.Errorln("Snapshot already exists having same name but different source volume")
			return nil, status.Errorf(codes.Internal, "Snapshot with same name already exists")
		}
	}
	glog.Infoln("Attempting to create snapshot", s.String())
	if err := s.Create(ctx); err != nil {
		return s, err
	}
	glog.Infoln("Successfully created snapshot", name, "snapId", s.GetId(), "for volume", v.GetId())
	return s, nil
}

// canBeOperated returns true if the object can be CRUDed without API backing (basically only dirVolume without snapshot)
func (v *UnifiedVolume) canBeOperated() error {
	if v.SnapshotUuid != nil {
		if v.apiClient == nil {
			if v.mounter.debugPath != "" {
				glog.Warningln("Assuming test execution, allowing without API")
			}
			return errors.New("Cannot operate volume of this type without API binding")
		}

		if !v.apiClient.SupportsFilesystemAsVolume() {
			return errors.New("volume of type Filesystem is not supported on current version of Weka cluster")
		}
	}
	return nil
}

func (v *UnifiedVolume) isMounted(xattr bool) bool {
	path := v.mountPath[xattr]
	if path != "" && PathIsWekaMount(path) {
		return true
	}
	return false
}

func (v *UnifiedVolume) GetMountPoint(xattr bool) (string, error) {
	if !v.isMounted(xattr) {
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
