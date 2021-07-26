package wekafs

import "github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"

type Volume interface {
	moveToTrash(mounter *wekaMounter, gc *dirVolumeGc) error
	getFullPath(mountPath string) string
	New(volumeId string, apiClient *apiclient.ApiClient) (Volume, error)
	updateCapacity(capacity int64) error
}
