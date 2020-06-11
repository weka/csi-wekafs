package wekafs

import (
	"github.com/golang/glog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
	"path/filepath"
	"syscall"
)

type dirVolume struct {
	id         string
	fs         string
	volumeType string
	dirName    string
}

func (v dirVolume) moveToTrash(mounter *wekaMounter) error {
	// TODO: Implement move to
	mountPath, err, unmount := mounter.Mount(v.fs)
	defer unmount()
	if err != nil {
		err = os.MkdirAll(filepath.Join(mountPath, garbagePath), 0750)
		if err != nil {
			return err
		}
		return os.Rename(v.getFullPath(mountPath), filepath.Join(mountPath, garbagePath, v.dirName))
	}
	return err
}

func (v dirVolume) getFullPath(mountPath string) string {
	return filepath.Join(mountPath, v.dirName)
}

func NewVolume(volumeId string) (dirVolume, error) {
	if err := validateVolumeId(volumeId); err != nil {
		return dirVolume{}, err
	}
	return dirVolume{
		id:         volumeId,
		fs:         GetFSName(volumeId),
		volumeType: GetVolumeType(volumeId),
		dirName:    GetVolumeDirName(volumeId),
	}, nil
}

func getMaxDirCapacity(mountPath string) (int64, error) {

	var stat syscall.Statfs_t
	err := syscall.Statfs(mountPath, &stat)
	if err != nil {
		return -1, status.Errorf(codes.FailedPrecondition, "Could not obtain free capacity on mount path %s", mountPath)
	}
	// Available blocks * size per block = available space in bytes
	return int64(stat.Bavail * uint64(stat.Bsize)), nil
}

func updateDirCapacity(volumePath string, capacity int64) error {
	glog.V(4).Infof("updating wekafs volume: %s", volumePath)
	m := make(map[string][]byte)
	m[xattrCapacity] = []byte(string(capacity))
	return updateXattrs(volumePath, m)
}
