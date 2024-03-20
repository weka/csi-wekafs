package wekafs

import (
	"context"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

type AnyServer interface {
	getMounter() AnyMounter
	getApiStore() *ApiStore
	getConfig() *DriverConfig
	isInDevMode() bool // TODO: Rename to isInDevMode
	getDefaultMountOptions() MountOptions
	getNodeId() string
}

type AnyMounter interface {
	NewMount(fsName string, options MountOptions) *wekaMount
	mountWithOptions(ctx context.Context, fsName string, mountOptions MountOptions, apiClient *apiclient.ApiClient) (string, error, UnmountFunc)
	Mount(ctx context.Context, fs string, apiClient *apiclient.ApiClient) (string, error, UnmountFunc)
	unmountWithOptions(ctx context.Context, fsName string, options MountOptions) error
	LogActiveMounts()
	gcInactiveMounts()
	schedulePeriodicMountGc()
	getGarbageCollector() *innerPathVolGc
}
