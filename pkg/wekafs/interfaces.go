package wekafs

import (
	"context"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/mount-utils"
	"sync"
	"time"
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
	LogActiveMounts()
	gcInactiveMounts()
	schedulePeriodicMountGc()
	getGarbageCollector() *innerPathVolGc
	getTransport() DataTransport
	getMountMap() *mountMap
	isEnabled() bool
	Enable()
	Disable()
	getSelinuxSupport() *bool
	setSelinuxSupport(bool)
}

type AnyMount interface {
	getMounter() AnyMounter
	getKMounter() mount.Interface
	isMounted() bool
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

type DataTransport string

func (transport DataTransport) String() string {
	return string(transport)
}

type UnmountFunc func()
