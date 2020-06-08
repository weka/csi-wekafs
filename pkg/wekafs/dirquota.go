package wekafs

import (
	"github.com/golang/glog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"syscall"
)

type dirVolume struct {
	id         string
	fs         string
	volumeType string
	dirName    string
}

func NewVolume(volumeId string) (dirVolume, error) {
	// TODO: Validate volumeId
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

func dirQuotaAsyncDelete(mounter *wekaMounter, fsname, dirname string) error {
	vol := dirVolumeGc{mounter: mounter}
	vol.triggerGcVolume(fsname, dirname)
	return nil
}
