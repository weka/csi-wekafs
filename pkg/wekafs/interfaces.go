package wekafs

import (
	"context"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/rs/zerolog"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// Volume represent an interface of single volume representation of any type
// the object can be instantiated (created on storage) or not yet
type Volume interface {
	Create(ctx context.Context, capacity int64) error
	CreateSnapshot(ctx context.Context, name string) (Snapshot, error)
	Delete(ctx context.Context) error
	EnsureRightCapacity(ctx context.Context, expectedCapacity int64) (bool, error)
	Exists(ctx context.Context) (bool, error)
	ExistsAndMatchesCapacity(ctx context.Context, capacity int64) (bool, bool, error)
	GetCapacity(ctx context.Context) (int64, error)
	GetId() string
	GetMountPoint(ctx context.Context, xattr bool) (string, error)
	GetType() VolumeType
	Mount(ctx context.Context, xattr bool) (error, UnmountFunc)
	SetParamsFromRequestParams(ctx context.Context, params map[string]string) error
	Unmount(ctx context.Context, xattr bool) error
	UpdateCapacity(ctx context.Context, enforceCapacity *bool, capacity int64) error
	UpdateParams(ctx context.Context) error
	canBeOperated() error
	getFilesystemObj(ctx context.Context) (*apiclient.FileSystem, error)
	getFullPath(ctx context.Context, xattr bool) string
	getInnerPath() string
	getMaxCapacity(ctx context.Context) (int64, error)
	isMounted(ctx context.Context, xattr bool) bool
	moveToTrash(ctx context.Context) error
	hasInnerPath() bool
	isOnSnapshot() bool
	getSnapshotObj(ctx context.Context) (*apiclient.Snapshot, error)
	MarshalZerologObject(e *zerolog.Event)
	getMountPath(xattr bool) string
	getCsiContentSource(ctx context.Context) *csi.VolumeContentSource
	initMountOptions(ctx context.Context)
	getMountOptions(ctx context.Context) MountOptions
	setMountOptions(ctx context.Context, mountOptions MountOptions)
}

// Snapshot represent an interface of single snapshot representation of any type
// the object can be instantiated (created on storage) or not yet
type Snapshot interface {
	Create(ctx context.Context) error
	Delete(ctx context.Context) error
	Exists(ctx context.Context) (bool, error)
	GetId() string
	getCsiSnapshot(ctx context.Context) *csi.Snapshot
	getInnerPath() string
	getObject(ctx context.Context) (*apiclient.Snapshot, error)
	getFileSystemObject(ctx context.Context) (*apiclient.FileSystem, error)
	hasInnerPath() bool
}

type AnyServer interface {
	getMounter() *wekaMounter
	getApiStore() *ApiStore
	getConfig() *DriverConfig
	isInDebugMode() bool
	getDefaultMountOptions() MountOptions
}
