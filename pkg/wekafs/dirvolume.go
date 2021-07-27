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

func (v DirVolume) getMaxCapacity(mountPath string) (int64, error) {
	glog.Infoln("Attempting to get max capacity available on filesystem", v.Filesystem)
	var stat syscall.Statfs_t
	err := syscall.Statfs(mountPath, &stat)
	if err != nil {
		return -1, status.Errorf(codes.FailedPrecondition, "Could not obtain free capacity on mount path %s", mountPath, err.Error())
	}
	// Available blocks * size per block = available space in bytes
	maxCapacity := int64(stat.Bavail * uint64(stat.Bsize))
	return maxCapacity, nil
}

func (v DirVolume) GetType() VolumeType {
	return VolumeTypeDirV1
}

func (v DirVolume) GetCapacity(mountPath string) (int64, error) {

	if v.apiClient == nil || !v.apiClient.SupportsQuotaDirectoryAsVolume() {
		// this is legacy volume, must treat xattrs...
		size, err := v.getSizeFromXattr(mountPath)
		if err != nil {
			return 0, err
		}
		return int64(size), nil

	}
	size, err := v.getSizeFromQuota(mountPath)
	if err != nil {
		return 0, err
	}
	return int64(size), nil
}

func (v DirVolume) UpdateCapacity(mountPath string, enforceCapacity *bool, capacityLimit int64) error {
	if v.apiClient == nil {
		glog.V(4).Infof("Volume has no API client bound, updating capacity in legacy mode")
		return v.updateCapacityXattr(mountPath, enforceCapacity, capacityLimit)
	}
	if !v.apiClient.SupportsQuotaDirectoryAsVolume() {
		glog.V(4).Infoln("Updating quota via API not supported by Weka cluster, updating capacity in legacy mode")
		return v.updateCapacityXattr(mountPath, enforceCapacity, capacityLimit)
	}
	return v.updateCapacityQuota(mountPath, enforceCapacity, capacityLimit)
}

func (v DirVolume) updateCapacityQuota(mountPath string, enforceCapacity *bool, capacityLimit int64) error {
	inodeId, err := v.getInodeId(mountPath)
	if err != nil {
		return err
	}
	fs, err := v.getFilesystemObj()
	if err != nil {
		return err
	}

	query := &apiclient.Quota{
		InodeId:       inodeId,
		FilesystemUid: fs.Uid,
	}

	q, err := v.apiClient.GetQuotaByFilter(query)
	// check if the quota already exists, otherwise return error
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}

	var quotaType apiclient.QuotaType
	if enforceCapacity != nil {
		if !*enforceCapacity {
			quotaType = apiclient.QuotaTypeSoft
		} else {
			quotaType = apiclient.QuotaTypeHard
		}
	} else {
		quotaType = q.QuotaType
	}

	if q.QuotaType != quotaType || q.CapacityLimit != uint64(capacityLimit) {
		r := apiclient.NewQuotaUpdateRequest(*fs, inodeId, quotaType, uint64(capacityLimit))
		return v.apiClient.UpdateQuota(r, q)
	}
	return nil
}

func (v DirVolume) updateCapacityXattr(mountPath string, enforceCapacity *bool, capacityLimit int64) error {
	if enforceCapacity != nil && *enforceCapacity {
		glog.V(3).Infof("Legacy volume does not support enforce capacity")
	}
	return setVolumeProperties(v.getFullPath(mountPath), capacityLimit, v.dirName)
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
	err = os.MkdirAll(garbageFullPath, 0750)
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
	if err := validateVolumeId(volumeId); err != nil {
		return DirVolume{}, err
	}
	if apiClient != nil {
		glog.V(4).Infof("Successfully bound volume to backend API %s@%s", apiClient.Username, apiClient.ClusterName)
	} else {
		glog.V(4).Infof("Volume was not bound to any backend API client")
	}
	return DirVolume{
		id:         volumeId,
		Filesystem: GetFSName(volumeId),
		volumeType: GetVolumeType(volumeId),
		dirName:    GetVolumeDirName(volumeId),
		apiClient:  apiClient,
	}, nil
}

//getInodeId used for obtaining the mount Path inode ID (to set quota on it later)
func (v DirVolume) getInodeId(mountPath string) (uint64, error) {
	fileinfo, _ := os.Stat(v.getFullPath(mountPath))
	stat, ok := fileinfo.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New(fmt.Sprintf("failed to obtain inodeId from %s", mountPath))
	}
	return stat.Ino, nil
}

func (v DirVolume) New(volumeId string, apiClient *apiclient.ApiClient) (Volume, error) {
	return NewVolume(volumeId, apiClient)
}

func (v DirVolume) GetId() string {
	return v.id
}
func (v DirVolume) CreateQuotaFromVolumeName(mountPath string, quotaType apiclient.QuotaType, capacityLimit uint64) (*apiclient.Quota, error) {
	fs, err := v.getFilesystemObj()
	if err != nil {
		return nil, err
	}
	inodeId, err := v.getInodeId(v.getFullPath(mountPath))
	if err != nil {
		return nil, errors.New("cannot set quota, could not find inode ID of the volume")
	}
	qr := apiclient.NewQuotaCreateRequest(*fs, inodeId, quotaType, capacityLimit)
	q := &apiclient.Quota{}
	if err := v.apiClient.CreateQuota(qr, q, false); err != nil {
		return nil, err
	}
	return q, nil
}

func (v DirVolume) getQuota(mountPath string) (*apiclient.Quota, error) {
	fs, err := v.getFilesystemObj()
	if err != nil {
		return nil, err
	}
	inodeId, err := v.getInodeId(mountPath)
	if err != nil {
		return nil, err
	}
	q := &apiclient.Quota{
		InodeId:       inodeId,
		FilesystemUid: fs.Uid,
	}
	return v.apiClient.GetQuotaByFilter(q)
}

func (v DirVolume) getSizeFromQuota(mountPath string) (uint64, error) {
	q, err := v.getQuota(mountPath)
	if err != nil {
		return 0, err
	}
	if q != nil {
		return q.CapacityLimit, nil
	}
	return 0, errors.New("could not fetch quota from API")
}

func (v DirVolume) getSizeFromXattr(mountPath string) (uint64, error) {
	if capacityString, err := xattr.Get(v.getFullPath(mountPath), xattrCapacity); err == nil {
		if capacity, err := strconv.ParseInt(string(capacityString), 10, 64); err == nil {
			return uint64(capacity), nil
		}
		return 0, nil
	}
	return 0, ErrNoXattrOnVolume
}

func (v DirVolume) getFilesystemObj() (*apiclient.FileSystem, error) {
	query := &apiclient.FileSystem{Name: v.Filesystem}
	fs, err := v.apiClient.GetFilesystemByFilter(query)
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
	if err := os.MkdirAll(volPath, 0750); err != nil {
		glog.Errorf("Failed to create directory %s", volPath)
		return err
	}
	// Update volume metadata on directory using xattrs
	err := v.UpdateCapacity(mountPath, &enforceCapacity, capacity)
	if err != nil {
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

func (v DirVolume) deleteQuota(mountPath string) error {
	if v.apiClient == nil || !v.apiClient.SupportsQuotaDirectoryAsVolume() {
		return apiclient.UnsupportedOperationError
	}
	fs, err := v.getFilesystemObj()
	if err != nil {
		return err
	}
	inodeId, err := v.getInodeId(mountPath)
	if err != nil {
		return err
	}
	qd := &apiclient.QuotaDeleteRequest{
		FilesystemUid: fs.Uid,
		InodeId:       inodeId,
	}
	return v.apiClient.DeleteQuota(qd)
}

func (v DirVolume) Delete(mountPath string) error {
	glog.V(4).Infof("Deleting volume %s, located in filesystem %s", v.id, v.Filesystem)
	volPath := v.getFullPath(mountPath)
	_ = os.RemoveAll(volPath)
	_ = v.deleteQuota(mountPath)
	_ = os.RemoveAll(volPath)
	glog.V(2).Infof("Deleted volume %s in :%v", v.id, volPath)
	return nil
}
