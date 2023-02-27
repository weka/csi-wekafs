package wekafs

type AnyServer interface {
	getMounter() *wekaMounter
	getApiStore() *ApiStore
	getConfig() *DriverConfig
	isInDebugMode() bool // TODO: Rename to isInDevMode
	getDefaultMountOptions() MountOptions
}
