package wekafs

import (
	"context"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"time"
)

const (
	dataTransportNfs    DataTransport = "nfs"
	dataTransportWekafs DataTransport = "wekafs"
)

type AnyServer interface {
	getMounter() AnyMounter
	getApiStore() *ApiStore
	getConfig() *DriverConfig
	isInDevMode() bool
	getDefaultMountOptions() MountOptions
	getNodeId() string
}

type AnyMounter interface {
	NewMount(fsName string, options MountOptions) AnyMount
	mountWithOptions(ctx context.Context, fsName string, mountOptions MountOptions, apiClient *apiclient.ApiClient) (string, error, UnmountFunc)
	Mount(ctx context.Context, fs string, apiClient *apiclient.ApiClient) (string, error, UnmountFunc)
	unmountWithOptions(ctx context.Context, fsName string, options MountOptions) error
	LogActiveMounts()
	gcInactiveMounts()
	schedulePeriodicMountGc()
	getGarbageCollector() *innerPathVolGc
	getTransport() DataTransport
}

type mountsMapPerFs map[string]AnyMount
type mountsMap map[string]mountsMapPerFs
type nfsMountsMap map[string]int // we only follow the mountPath and number of references
type DataTransport string
type UnmountFunc func()

type AnyMount interface {
	isInDevMode() bool
	isMounted() bool
	incRef(ctx context.Context, apiClient *apiclient.ApiClient) error
	decRef(ctx context.Context) error
	getRefCount() int
	doUnmount(ctx context.Context) error
	doMount(ctx context.Context, apiClient *apiclient.ApiClient, mountOptions MountOptions) error
	getMountPoint() string
	getMountOptions() MountOptions
	getLastUsed() time.Time
	locateMountIP() error // used only for NFS
}
