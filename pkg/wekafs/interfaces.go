package wekafs

import (
	"context"
	"time"

	"github.com/wekafs/csi-wekafs/pkg/volumeid"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

type AnyServer interface {
	getMounter(ctx context.Context) AnyMounter
	// getMounterByTransport returns the mounter for one specific transport, which is what unmounting
	// an existing mount has to use: the transport a volume was mounted with is a property of that
	// mount, not of whichever transport happens to be preferred now.
	getMounterByTransport(ctx context.Context, transport DataTransport) AnyMounter
	getApiStore() *ApiStore
	getConfig() *DriverConfig
	getDefaultMountOptions() MountOptions
	getNodeId() string
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
	// Enable and Disable gate a mounter at runtime. Both mounters exist for the whole process life
	// so that one node can serve some volumes over wekafs and others over NFS, and Probe flips these
	// as the Weka client comes and goes underneath.
	Enable()
	Disable()
	isEnabled() bool
}

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
	isMounted(ctx context.Context) bool
	incRef(ctx context.Context, apiClient *apiclient.ApiClient) error
	decRef(ctx context.Context) error
	doUnmount(ctx context.Context) error
	doMount(ctx context.Context, apiClient *apiclient.ApiClient, mountOptions MountOptions) error
	getMountPoint() string
	getMountOptions() MountOptions
	getRefcountIdx() string
	getLastUsed() time.Time
}

// VolumeType aliases volumeid.Type so that the driver and out-of-tree tooling such as the
// migrator share a single definition of the volume handle format.
type VolumeType = volumeid.Type

// VolumeBackingType classifies how a volume is physically backed (plain filesystem, legacy
// directory, snapshot, or a directory rooted on a snapshot), used only as a metrics label.
type VolumeBackingType string

type CsiPluginMode string
