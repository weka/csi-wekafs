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
	Exists(ctx context.Context) (bool, error)
	GetCapacity(ctx context.Context) (int64, error)
	GetId() string
	GetType() VolumeType
	MountUnderlyingFS(ctx context.Context, xattr bool) (error, UnmountFunc)
	// ObtainRequestParams updates Volume with params passed as part of CSI operation requests
	ObtainRequestParams(ctx context.Context, params map[string]string) error
	// UnmountUnderlyingFS unmounts the filesystem on which volume resides
	UnmountUnderlyingFS(ctx context.Context, xattr bool) error
	UpdateCapacity(ctx context.Context, enforceCapacity *bool, capacity int64) error
	// CanBeOperated returns error if volume has no secrets attached but requires API operations to be done
	CanBeOperated() error
	// GetFullPath returns a full path on which the volume contents is accessible
	GetFullPath(ctx context.Context, xattr bool) string
	// Trash starts deletion of the volume. May be synchronous or asynchronous, depends on implementation
	Trash(ctx context.Context) error
	isOnSnapshot() bool
	getSnapshotObj(ctx context.Context) (*apiclient.Snapshot, error)
	MarshalZerologObject(e *zerolog.Event)
	getCsiContentSource(ctx context.Context) *csi.VolumeContentSource
	initMountOptions(ctx context.Context)
	getMountOptions(ctx context.Context) MountOptions
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
