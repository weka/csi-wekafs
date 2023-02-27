package wekafs

import (
	"context"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

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
