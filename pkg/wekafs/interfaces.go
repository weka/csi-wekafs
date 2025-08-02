package wekafs

import (
	"context"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"sync"
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
	getBackgroundTasksWg() *sync.WaitGroup
}

type AnyMounter interface {
	NewMount(fsName string, options MountOptions) AnyMount
	mountWithOptions(ctx context.Context, fsName string, mountOptions MountOptions, apiClient *apiclient.ApiClient) (string, error, UnmountFunc)
	Mount(ctx context.Context, fs string, apiClient *apiclient.ApiClient) (string, error, UnmountFunc)
	unmountWithOptions(ctx context.Context, fsName string, options MountOptions) error
	LogActiveMounts(ctx context.Context)
	gcInactiveMounts(ctx context.Context)
	schedulePeriodicMountGc(ctx context.Context)
	getGarbageCollector() *innerPathVolGc
	getTransport() DataTransport
}

type nfsMountsMap map[string]int // we only follow the mountPath and number of references
type wekafsMountsMap map[string]int
type DataTransport string
type UnmountFunc func() error

// NoOpUnmount is a no-op UnmountFunc returned on error paths where no mount succeeded.
var NoOpUnmount UnmountFunc = func() error { return nil }

// deferUmount calls fn and, if it returns an error and *retErr is nil, assigns the error to *retErr.
// Use with named return values: defer deferUmount(unmount, &retErr)
func deferUmount(fn UnmountFunc, retErr *error) {
	if uErr := fn(); uErr != nil && *retErr == nil {
		*retErr = uErr
	}
}

type AnyMount interface {
	isInDevMode() bool
	isMounted(ctx context.Context) bool
	incRef(ctx context.Context, apiClient *apiclient.ApiClient) error
	decRef(ctx context.Context) error
	getRefCount() int
	doUnmount(ctx context.Context) error
	doMount(ctx context.Context, apiClient *apiclient.ApiClient, mountOptions MountOptions) error
	getMountPoint() string
	getMountOptions() MountOptions
	getLastUsed() time.Time
}
