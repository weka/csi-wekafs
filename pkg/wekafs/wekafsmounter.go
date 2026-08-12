package wekafs

import (
	"context"
	"path"

	"github.com/rs/zerolog/log"
	"k8s.io/mount-utils"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

type wekafsMounter struct {
	mountMap     *mountMap
	kMounter     mount.Interface
	gc           *innerPathVolGc
	config       *DriverConfig
	mountBaseDir string
	mounterState
}

// mountBaseDirForRole gives each transport its own directory under the role's base. Both mounters
// now run at once, and both name a mount {fsName}-{sha1(fsName:options)}, so without this separation
// an NFS and a wekafs mount of the same filesystem would land on the same path whenever their option
// strings happened to match - and unmounting either would tear down the other.
func mountBaseDirForRole(mode CsiPluginMode, transport DataTransport) string {
	var base string
	switch mode {
	case CsiModeNode:
		base = "/run/weka-fs-mounts-node"
	case CsiModeController:
		base = "/run/weka-fs-mounts-controller"
	case CsiModeMetricsServer:
		// Mounters are only ever built when csiMode.servesCsiGrpc() is true (see driver.go), which
		// excludes CsiModeMetricsServer, so this base is never actually used in production - listed
		// explicitly so the switch stays exhaustive over every CsiPluginMode rather than relying on a
		// silent catch-all.
		base = "/run/weka-fs-mounts"
	default:
		log.Warn().Str("mode", string(mode)).Msg("mountBaseDirForRole: unrecognized CSI plugin mode, using generic mount base dir")
		base = "/run/weka-fs-mounts"
	}
	return path.Join(base, string(transport))
}

func (m *wekafsMounter) getGarbageCollector() *innerPathVolGc {
	return m.gc
}

func newWekafsMounter(ctx context.Context, driver *WekaFsDriver) *wekafsMounter {
	mounter := &wekafsMounter{mountMap: newMountMap(), config: driver.config, mountBaseDir: mountBaseDirForRole(driver.csiMode, dataTransportWekafs)}
	if driver.selinuxSupport {
		log.Ctx(ctx).Debug().Msg("SELinux support is forced")
		mounter.forceSelinux()
	}
	mounter.gc = initInnerPathVolumeGc(mounter)
	mounter.gc.config = driver.config
	mounter.schedulePeriodicMountGc(ctx)

	return mounter
}

func (m *wekafsMounter) NewMount(fsName string, options MountOptions) AnyMount {
	if m.kMounter == nil {
		m.kMounter = mount.New("")
	}
	uniqueId := getStringSha1AsB32(fsName + ":" + options.String())
	wMount := &wekafsMount{
		mounter:      m,
		kMounter:     m.kMounter,
		fsName:       fsName,
		mountPoint:   m.mountBaseDir + "/" + getAsciiPart(fsName, 64) + "-" + uniqueId,
		mountOptions: options,
	}
	return wMount
}

func (m *wekafsMounter) mountWithOptions(ctx context.Context, fsName string, mountOptions MountOptions, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	mountOptions.setSelinux(m.getSelinuxStatus(ctx), MountProtocolWekafs)
	mountObj := m.NewMount(fsName, mountOptions).(*wekafsMount)

	if err := mountObj.ensureLocalContainerName(ctx, apiClient); err != nil {
		return "", err, NoOpUnmount
	}

	mountErr := mountObj.incRef(ctx, apiClient)

	if mountErr != nil {
		log.Ctx(ctx).Error().Err(mountErr).Msg("Failed mounting")
		return "", mountErr, NoOpUnmount
	}
	return mountObj.getMountPoint(), nil, func() error {
		if mountErr == nil {
			return mountObj.decRef(ctx)
		}
		return nil
	}
}

func (m *wekafsMounter) Mount(ctx context.Context, fs string, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	return m.mountWithOptions(ctx, fs, getDefaultMountOptions(), apiClient)
}

func (m *wekafsMounter) unmountWithOptions(ctx context.Context, fsName string, options MountOptions) error {
	options.setSelinux(m.getSelinuxStatus(ctx), MountProtocolWekafs)
	log.Ctx(ctx).Trace().Strs("mount_options", options.Strings()).Str("filesystem", fsName).Msg("Received an unmount request")
	mnt := m.NewMount(fsName, options).(*wekafsMount)

	err := mnt.locateContainerName()
	if err != nil {
		log.Ctx(ctx).Error().Err(err).Msg("Failed to locate containerName")
		return err
	}

	return mnt.decRef(ctx)
}

func (m *wekafsMounter) LogActiveMounts(ctx context.Context) {
	anyMounterLogActiveMounts(ctx, m.mountMap, m.getTransport())
}

func (m *wekafsMounter) gcInactiveMounts(ctx context.Context) {
	anyMounterGcInactiveMounts(ctx, m.mountMap)
}

func (m *wekafsMounter) schedulePeriodicMountGc(ctx context.Context) {
	anyMounterSchedulePeriodicMountGc(ctx, m)
}

func (m *wekafsMounter) getTransport() DataTransport {
	return dataTransportWekafs
}
