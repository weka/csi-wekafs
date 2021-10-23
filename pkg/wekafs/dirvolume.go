package wekafs

import (
	"errors"
	"fmt"
	"github.com/golang/glog"
	"github.com/google/uuid"
	"github.com/pkg/xattr"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

type DirVolume struct {
	id         string
	Filesystem string
	volumeType string
	dirName    string
	apiClient  *apiclient.ApiClient
}

var ErrNoXattrOnVolume = errors.New("xattr not set on volume")
var ErrBadXattrOnVolume = errors.New("could not parse xattr on volume")

func (v DirVolume) getMaxCapacity(mountPath string) (int64, error) {
	glog.V(5).Infoln("Attempting to get max capacity available on filesystem", v.Filesystem)
	var stat syscall.Statfs_t
	err := syscall.Statfs(mountPath, &stat)
	if err != nil {
		return -1, status.Errorf(codes.FailedPrecondition, "Could not obtain free capacity on mount path %s: %s", mountPath, err.Error())
	}
	// Available blocks * size per block = available space in bytes
	maxCapacity := int64(stat.Bavail * uint64(stat.Bsize))
	glog.V(4).Infoln("Max capacity available for a volume:", maxCapacity)
	return maxCapacity, nil
}

func (v DirVolume) GetType() VolumeType {
	return VolumeTypeDirV1
}

func (v DirVolume) GetCapacity(mountPath string) (int64, error) {
	glog.V(3).Infoln("Attempting to get current capacity of volume", v.GetId())
	if v.apiClient != nil && v.apiClient.SupportsQuotaDirectoryAsVolume() {
		size, err := v.getSizeFromQuota(mountPath)
		if err == nil {
			glog.V(3).Infoln("Current capacity of volume", v.GetId(), "is", size, "obtained via API")
			return int64(size), nil
		}
		//if err == apiclient.ObjectNotFoundError {
		//	glog.V(3).Infoln("Volume has no quota, returning capacity 0")
		//	return 0, nil //
		//}
	}
	glog.V(4).Infoln("Volume", v.GetId(), "appears to be a legacy volume. Trying to fetch capacity from Xattr")
	size, err := v.getSizeFromXattr(mountPath)
	if err != nil {
		return 0, err
	}
	glog.V(3).Infoln("Current capacity of volume", v.GetId(), "is", size, "obtained via Xattr")
	return int64(size), nil
}

func (v DirVolume) UpdateCapacity(mountPath string, enforceCapacity *bool, capacityLimit int64) error {
	glog.V(3).Infoln("Updating capacity of the volume", v.GetId(), "to", capacityLimit)
	var fallback = true
	f := func() error { return v.updateCapacityQuota(mountPath, enforceCapacity, capacityLimit) }
	if v.apiClient == nil {
		glog.V(4).Infof("Volume has no API client bound, updating capacity in legacy mode")
		f = func() error { return v.updateCapacityXattr(mountPath, enforceCapacity, capacityLimit) }
		fallback = false
	} else if !v.apiClient.SupportsQuotaDirectoryAsVolume() {
		glog.V(4).Infoln("Updating quota via API not supported by Weka cluster, updating capacity in legacy mode")
		f = func() error { return v.updateCapacityXattr(mountPath, enforceCapacity, capacityLimit) }
		fallback = false
	}
	err := f()
	if err == nil {
		glog.V(3).Infoln("Successfully updated capacity for volume", v.GetId())
		return err
	}
	if fallback {
		glog.V(4).Infoln("Failed to set quota for a volume, maybe it is not empty? Falling back to xattr")
		f = func() error { return v.updateCapacityXattr(mountPath, enforceCapacity, capacityLimit) }
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

func (v DirVolume) updateCapacityQuota(mountPath string, enforceCapacity *bool, capacityLimit int64) error {
	if enforceCapacity != nil {
		glog.V(4).Infoln("Updating quota on volume", v.GetId(), "to", capacityLimit, "enforceCapacity:", *enforceCapacity)
	} else {
		glog.V(4).Infoln("Updating quota on volume", v.GetId(), "to", capacityLimit, "enforceCapacity:", "RETAIN")
	}
	inodeId, err := v.getInodeId(mountPath)
	if err != nil {
		glog.Errorln("Failed to fetch inode ID for volume", v.GetId())
		return err
	}
	fs, err := v.getFilesystemObj()
	if err != nil {
		glog.Errorln("Failed to fetch filesystem for volume", v.GetId())
		return err
	}

	// check if the quota already exists. If not - create it and exit
	q, err := v.apiClient.GetQuotaByFileSystemAndInode(fs, inodeId)
	if err != nil {
		if err != apiclient.ObjectNotFoundError {
			// any other error
			glog.Errorln("Failed to get quota:", err)
			return status.Error(codes.Internal, err.Error())
		}
		_, err := v.CreateQuotaFromMountPath(mountPath, enforceCapacity, uint64(capacityLimit))
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
	r := apiclient.NewQuotaUpdateRequest(*fs, inodeId, quotaType, uint64(capacityLimit))
	glog.V(5).Infoln("Constructed update request", r.String())
	err = v.apiClient.UpdateQuota(r, updatedQuota)
	if err != nil {
		glog.V(4).Infoln("Failed to set quota on volume", v.GetId(), "to", updatedQuota.GetQuotaType(), updatedQuota.GetCapacityLimit(), err)
		return err
	}
	glog.V(4).Infoln("Successfully set quota on volume", v.GetId(), "to", updatedQuota.GetQuotaType(), updatedQuota.GetCapacityLimit())
	return nil
}

func (v DirVolume) updateCapacityXattr(mountPath string, enforceCapacity *bool, capacityLimit int64) error {
	glog.V(4).Infoln("Updating xattrs on volume", v.GetId(), "to", capacityLimit, "enforce:", enforceCapacity)
	if enforceCapacity != nil && *enforceCapacity {
		glog.V(3).Infof("Legacy volume does not support enforce capacity")
	}
	err := setVolumeProperties(v.getFullPath(mountPath), capacityLimit, v.dirName)
	if err != nil {
		glog.Errorln("Failed to update xattrs on volume", v.GetId(), "capacity is not set")
	}
	return err
}

func (v DirVolume) moveToTrash(mounter *wekaMounter, gc *dirVolumeGc) error {
	mountPath, err, unmount := v.Mount(mounter, false)
	defer unmount()
	if err != nil {
		glog.Errorf("Error mounting %s for deletion %s", v.id, err)
		return err
	}
	garbageFullPath := filepath.Join(mountPath, garbagePath)
	glog.Infof("Ensuring that garbagePath %s exists", garbageFullPath)
	err = os.MkdirAll(garbageFullPath, DefaultVolumePermissions)
	if err != nil {
		glog.Errorf("Failed to create garbagePath %s", garbageFullPath)
		return err
	}
	u, _ := uuid.NewUUID()
	volumeTrashLoc := filepath.Join(garbageFullPath, u.String())
	glog.Infof("Attempting to move volume %s %s -> %s", v.id, v.getFullPath(mountPath), volumeTrashLoc)
	if err = os.Rename(v.getFullPath(mountPath), volumeTrashLoc); err == nil {
		v.dirName = u.String()
		gc.triggerGcVolume(v) // TODO: Better to preserve immutability some way , needed due to recreation of volumes with same name
		glog.V(4).Infof("Moved %s to trash", v.id)
		return err
	} else {
		glog.V(4).Infof("Failed moving %s to trash: %s", v.getFullPath(mountPath), err)
		return err
	}
}

func (v DirVolume) getFullPath(mountPath string) string {
	return filepath.Join(mountPath, v.dirName)
}

func NewVolume(volumeId string, apiClient *apiclient.ApiClient) (Volume, error) {
	glog.V(5).Infoln("Creating new volume representation object for volume ID", volumeId)
	if err := validateVolumeId(volumeId); err != nil {
		return &DirVolume{}, err
	}
	if apiClient != nil {
		glog.V(5).Infof("Successfully bound volume to backend API %s@%s", apiClient.Username, apiClient.ClusterName)
	} else {
		glog.V(5).Infof("Volume was not bound to any backend API client")
	}
	return &DirVolume{
		id:         volumeId,
		Filesystem: GetFSName(volumeId),
		volumeType: GetVolumeType(volumeId),
		dirName:    GetVolumeDirName(volumeId),
		apiClient:  apiClient,
	}, nil
}

//getInodeId used for obtaining the mount Path inode ID (to set quota on it later)
func (v DirVolume) getInodeId(mountPath string) (uint64, error) {
	glog.V(5).Infoln("Getting inode ID of volume", v.GetId(), "fullpath: ", v.getFullPath(mountPath))
	fileInfo, err := os.Stat(v.getFullPath(mountPath))
	if err != nil {
		glog.Error(err)
		return 0, err
	}
	stat, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New(fmt.Sprintf("failed to obtain inodeId from %s", mountPath))
	}
	glog.V(5).Infoln("Inode ID of the volume", v.GetId(), "is", stat.Ino)
	return stat.Ino, nil
}

func (v DirVolume) GetId() string {
	return v.id
}

func (v DirVolume) CreateQuotaFromMountPath(mountPath string, enforceCapacity *bool, capacityLimit uint64) (*apiclient.Quota, error) {
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

	fs, err := v.getFilesystemObj()
	if err != nil {
		return nil, err
	}
	inodeId, err := v.getInodeId(mountPath)
	if err != nil {
		return nil, errors.New("cannot set quota, could not find inode ID of the volume")
	}
	qr := apiclient.NewQuotaCreateRequest(*fs, inodeId, quotaType, capacityLimit)
	q := &apiclient.Quota{}
	if err := v.apiClient.CreateQuota(qr, q, true); err != nil {
		return nil, err
	}
	glog.V(4).Infoln("Quota successfully set for volume", v.GetId())
	return q, nil
}

func (v DirVolume) getQuota(mountPath string) (*apiclient.Quota, error) {
	glog.V(4).Infoln("Getting existing quota for volume", v.GetId())
	fs, err := v.getFilesystemObj()
	if err != nil {
		return nil, err
	}
	inodeId, err := v.getInodeId(mountPath)
	if err != nil {
		return nil, err
	}
	ret, err := v.apiClient.GetQuotaByFileSystemAndInode(fs, inodeId)
	if ret != nil {
		glog.V(4).Infoln("Successfully acquired existing quota for volume", v.GetId(), ret.GetQuotaType(), ret.GetCapacityLimit())
	}
	return ret, err
}

func (v DirVolume) getSizeFromQuota(mountPath string) (uint64, error) {
	q, err := v.getQuota(mountPath)
	if err != nil {
		return 0, err
	}
	if q != nil {
		return q.GetCapacityLimit(), nil
	}
	return 0, errors.New("could not fetch quota from API")
}

func (v DirVolume) getSizeFromXattr(mountPath string) (uint64, error) {
	if capacityString, err := xattr.Get(v.getFullPath(mountPath), xattrCapacity); err == nil {
		if capacity, err := strconv.ParseInt(string(capacityString), 10, 64); err == nil {
			return uint64(capacity), nil
		}
		return 0, ErrBadXattrOnVolume
	}
	return 0, ErrNoXattrOnVolume
}

func (v DirVolume) getFilesystemObj() (*apiclient.FileSystem, error) {
	fs, err := v.apiClient.GetFileSystemByName(v.Filesystem)
	if err != nil {
		return nil, err
	}
	return fs, nil
}

func (v DirVolume) Mount(mounter *wekaMounter, xattr bool) (string, error, UnmountFunc) {
	if xattr {
		return mounter.MountXattr(v.Filesystem)
	}
	return mounter.Mount(v.Filesystem)
}

func (v DirVolume) Unmount(mounter *wekaMounter) error {
	return mounter.Unmount(v.Filesystem)
}

func (v DirVolume) Exists(mountPoint string) (bool, error) {
	if !PathExists(v.getFullPath(mountPoint)) {
		glog.Infof("Volume %s not found on filesystem %s", v.GetId(), v.Filesystem)
		return false, nil
	}
	if err := pathIsDirectory(v.getFullPath(mountPoint)); err != nil {
		glog.Errorf("Volume %s is unusable: path %s is a not a directory", v.GetId(), v.Filesystem)
		return false, status.Error(codes.Internal, err.Error())
	}
	glog.Infof("Volume %s exists and accessible via %s", v.id, v.getFullPath(mountPoint))
	return true, nil
}

func (v DirVolume) Create(mountPath string, enforceCapacity bool, capacity int64) error {
	volPath := v.getFullPath(mountPath)
	if err := os.MkdirAll(volPath, DefaultVolumePermissions); err != nil {
		glog.Errorf("Failed to create directory %s", volPath)
		return err
	}

	glog.Infof("Created directory %s, updating its capacity to %v", v.getFullPath(mountPath), capacity)
	// Update volume metadata on directory using xattrs
	if err := v.UpdateCapacity(mountPath, &enforceCapacity, capacity); err != nil {
		glog.Warningf("Failed to update capacity on newly created volume %s in: %s, deleting", v.id, volPath)
		err2 := v.Delete(mountPath)
		if err2 != nil {
			glog.V(2).Infof("Failed to clean up directory %s after unsuccessful set capacity", v.dirName)
		}
		return err
	}
	glog.V(3).Infof("Created volume %s in: %v", v.id, volPath)
	return nil
}

// Delete is a synchronous delete, used for cleanup on unsuccessful ControllerCreateVolume. GC flow is separate
func (v DirVolume) Delete(mountPath string) error {
	glog.V(4).Infof("Deleting volume %s, located in filesystem %s", v.id, v.Filesystem)
	volPath := v.getFullPath(mountPath)
	_ = os.RemoveAll(volPath)
	glog.V(2).Infof("Deleted volume %s in :%v", v.id, volPath)
	return nil
}
