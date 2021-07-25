package wekafs

type Volume interface {
	moveToTrash(mounter *wekaMounter, gc *dirVolumeGc) error
	getFullPath(mountPath string) string
	New(volumeId string) (Volume, error)
	updateCapacity(capacity int64) error
}
