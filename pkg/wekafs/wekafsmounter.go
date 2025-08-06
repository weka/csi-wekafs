package wekafs

import (
	"context"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/mount-utils"
	"path"
)

type wekafsMounter struct {
	mountMap                *mountMap
	kMounter                mount.Interface
	selinuxSupport          *bool
	gc                      *innerPathVolGc
	allowProtocolContainers bool
	enabled                 bool
}

func (m *wekafsMounter) setSelinuxSupport(b bool) {
	m.selinuxSupport = &b
}

func (m *wekafsMounter) getSelinuxSupport() *bool {
	return m.selinuxSupport
}

func (m *wekafsMounter) getMountMap() *mountMap {
	return m.mountMap
}

func (m *wekafsMounter) isEnabled() bool {
	return m.enabled
}

func (m *wekafsMounter) Enable() {
	if !m.enabled {
		log.Ctx(context.Background()).Info().Msg("Enabling WekaFS mounter")
	}
	m.enabled = true
}

func (m *wekafsMounter) Disable() {
	if m.enabled {
		log.Ctx(context.Background()).Info().Msg("Disabling WekaFS mounter")
	}
	m.enabled = false
}

func (m *wekafsMounter) getGarbageCollector() *innerPathVolGc {
	return m.gc
}

func (m *wekafsMounter) getSelinuxStatus(ctx context.Context) bool {
	return anyMounterGetSelinuxStatus(ctx, m)
}

func (m *wekafsMounter) LogActiveMounts() {
	anyMounterLogActiveMounts(m)
}

func (m *wekafsMounter) gcInactiveMounts() {
	anyMounterGcInactiveMounts(m)
}

func (m *wekafsMounter) schedulePeriodicMountGc() {
	anyMounterSchedulePeriodicMountGc(m)
}

func (m *wekafsMounter) getTransport() DataTransport {
	return dataTransportWekafs
}

func newWekafsMounter(driver *WekaFsDriver) *wekafsMounter {
	var selinuxSupport *bool
	if driver.selinuxSupport {
		log.Debug().Msg("SELinux support is forced")
		selinuxSupport = &[]bool{true}[0]
	}
	mounter := &wekafsMounter{mountMap: newMountMap(), selinuxSupport: selinuxSupport, enabled: true}
	mounter.gc = initInnerPathVolumeGc(mounter)
	mounter.gc.config = driver.config
	mounter.schedulePeriodicMountGc()

	return mounter
}

func (m *wekafsMounter) NewMount(fsName string, options MountOptions) AnyMount {
	if m.kMounter == nil {
		m.kMounter = mount.New("")
	}
	uniqueId := getStringSha1AsB32(fsName + ":" + options.String())
	wMount := &wekafsMount{
		mounter:                 m,
		kMounter:                m.kMounter,
		fsName:                  fsName,
		mountPoint:              path.Join(MountBasePath, m.getTransport().String(), getAsciiPart(fsName, 64)+"-"+uniqueId),
		mountOptions:            options,
		allowProtocolContainers: m.allowProtocolContainers,
	}
	return wMount
}

func (m *wekafsMounter) mountWithOptions(ctx context.Context, fsName string, mountOptions MountOptions, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	mountOptions.setSelinux(m.getSelinuxStatus(ctx), MountProtocolWekafs)
	mountObj := m.NewMount(fsName, mountOptions).(*wekafsMount)

	if err := mountObj.ensureLocalContainerName(ctx, apiClient); err != nil {
		return "", err, func() {}
	}

	mountErr := mountObj.incRef(ctx, apiClient)

	if mountErr != nil {
		log.Ctx(ctx).Error().Err(mountErr).Msg("Failed mounting")
		return "", mountErr, func() {}
	}
	return mountObj.getMountPoint(), nil, func() {
		if mountErr == nil {
			_ = mountObj.decRef(ctx)
		}
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
