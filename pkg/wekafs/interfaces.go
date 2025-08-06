package wekafs

import (
	"context"
	"sync"
	"time"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"go.uber.org/atomic"
	"k8s.io/mount-utils"
)

type AnyServer interface {
	getMounter(ctx context.Context) AnyMounter
	getMounterByTransport(ctx context.Context, transport DataTransport) AnyMounter
	getApiStore() *ApiStore
	getConfig() *DriverConfig
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
	getMountMap() *mountMap
	isEnabled() bool
	Enable()
	Disable()
	getSelinuxSupport() *bool
	setSelinuxSupport(bool)
}

type nfsMountsMap map[string]*atomic.Int32 // we only follow the mountPath and number of references
type wekafsMountsMap map[string]*atomic.Int32
type DataTransport string

func (dt DataTransport) Unknown() bool {
	return dt == ""
}

func (dt DataTransport) String() string {
	return string(dt)
}

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
	getMounter() AnyMounter
	getKMounter() mount.Interface
	isMounted(ctx context.Context) bool
	incRef(ctx context.Context, apiClient *apiclient.ApiClient) error
	decRef(ctx context.Context) error
	doUnmount(ctx context.Context) error
	doMount(ctx context.Context, apiClient *apiclient.ApiClient, mountOptions MountOptions) error
	getMountPoint() string
	getMountOptions() MountOptions
	getLastUsed() time.Time
	getRefCountIndex() string
	getFsName() string
}

type VolumeBackingType string
type VolumeType string
type CsiPluginMode string
