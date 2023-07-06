package wekafs

type AnyServer interface {
	getMounter() *wekaMounter
	getApiStore() *ApiStore
	getConfig() *DriverConfig
	isInDevMode() bool // TODO: Rename to isInDevMode
	getDefaultMountOptions() MountOptions
	getNodeId() string
}
