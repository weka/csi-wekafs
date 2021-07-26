package wekafs

import "github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"

type Volume interface {
	GetType() VolumeType
	GetId() string
	moveToTrash(mounter *wekaMounter, gc *dirVolumeGc) error
	getFullPath(mountPath string) string
	New(volumeId string, apiClient *apiclient.ApiClient) (Volume, error)
	UpdateCapacity(mountPath string, enforceCapacity *bool, capacity int64) error
	GetCapacity(mountPath string) (int64, error)
	Mount(mounter *wekaMounter, xattr bool) (string, error, UnmountFunc)
	Unmount(mounter *wekaMounter) error
	Exists(mountPath string) (bool, error)
	getMaxCapacity(mountPath string) (int64, error)
	Create(mountPath string, enforceCapacity bool, capacity int64) error
	Delete(mountPath string) error
}
