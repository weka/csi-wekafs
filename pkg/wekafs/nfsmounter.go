package wekafs

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/mount-utils"
	"path"
)

type nfsMounter struct {
	mountMap              *mountMap
	kMounter              mount.Interface
	selinuxSupport        *bool
	gc                    *innerPathVolGc
	clientGroupName       string
	nfsProtocolVersion    string
	exclusiveMountOptions []mutuallyExclusiveMountOptionSet
	enabled               bool
}

func (m *nfsMounter) setSelinuxSupport(b bool) {
	m.selinuxSupport = &b
}

func (m *nfsMounter) getSelinuxSupport() *bool {
	return m.selinuxSupport
}

func (m *nfsMounter) getMountMap() *mountMap {
	return m.mountMap
}

func (m *nfsMounter) isEnabled() bool {
	return m.enabled
}

func (m *nfsMounter) Enable() {
	if !m.enabled {
		log.Ctx(context.Background()).Info().Msg("Enabling NFS mounter")
	}
	m.enabled = true
}

func (m *nfsMounter) Disable() {
	if m.enabled {
		log.Ctx(context.Background()).Info().Msg("Disabling NFS mounter")
	}
	m.enabled = false
}

func (m *nfsMounter) getGarbageCollector() *innerPathVolGc {
	return m.gc
}

func (m *nfsMounter) getSelinuxStatus(ctx context.Context) bool {
	return anyMounterGetSelinuxStatus(ctx, m)
}

func (m *nfsMounter) LogActiveMounts() {
	anyMounterLogActiveMounts(m)
}

func (m *nfsMounter) gcInactiveMounts() {
	anyMounterGcInactiveMounts(m)
}

func (m *nfsMounter) schedulePeriodicMountGc() {
	anyMounterSchedulePeriodicMountGc(m)
}

func (m *nfsMounter) getTransport() DataTransport {
	return dataTransportNfs
}

func newNfsMounter(driver *WekaFsDriver) *nfsMounter {
	var selinuxSupport *bool
	if driver.selinuxSupport {
		log.Debug().Msg("SELinux support is forced")
		selinuxSupport = &[]bool{true}[0]
	}
	mounter := &nfsMounter{mountMap: newMountMap(), selinuxSupport: selinuxSupport, exclusiveMountOptions: driver.config.mutuallyExclusiveOptions, enabled: false}
	mounter.gc = initInnerPathVolumeGc(mounter)
	mounter.gc.config = driver.config
	mounter.schedulePeriodicMountGc()
	mounter.clientGroupName = driver.config.clientGroupName
	mounter.nfsProtocolVersion = driver.config.nfsProtocolVersion
	return mounter
}

func (m *nfsMounter) NewMount(fsName string, options MountOptions) AnyMount {
	if m.kMounter == nil {
		m.kMounter = mount.New("")
	}
	uniqueId := getStringSha1AsB32(fsName + ":" + options.String())
	wMount := &nfsMount{
		mounter:         m,
		kMounter:        m.kMounter,
		fsName:          fsName,
		mountPoint:      path.Join(MountBasePath, m.getTransport().String(), getAsciiPart(fsName, 64)+"-"+uniqueId),
		mountOptions:    options,
		clientGroupName: m.clientGroupName,
		protocolVersion: apiclient.NfsVersionString(fmt.Sprintf("V%s", m.nfsProtocolVersion)),
	}
	return wMount
}

func (m *nfsMounter) mountWithOptions(ctx context.Context, fsName string, mountOptions MountOptions, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	mountOptions.setSelinux(m.getSelinuxStatus(ctx), MountProtocolNfs)
	mountOptions = mountOptions.AsNfs()
	mountOptions.Merge(mountOptions, m.exclusiveMountOptions)
	mountObj := m.NewMount(fsName, mountOptions).(*nfsMount)

	if err := mountObj.ensureMountIpAddress(ctx, apiClient); err != nil {
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

func (m *nfsMounter) Mount(ctx context.Context, fs string, apiClient *apiclient.ApiClient) (string, error, UnmountFunc) {
	return m.mountWithOptions(ctx, fs, getDefaultMountOptions(), apiClient)
}

func (m *nfsMounter) unmountWithOptions(ctx context.Context, fsName string, options MountOptions) error {
	options.setSelinux(m.getSelinuxStatus(ctx), MountProtocolNfs)
	options = options.AsNfs()
	options.Merge(options, m.exclusiveMountOptions)
	log.Ctx(ctx).Trace().Strs("mount_options", options.Strings()).Str("filesystem", fsName).Msg("Received an unmount request")
	mnt := m.NewMount(fsName, options).(*nfsMount)
	// since we are not aware of the IP address of the mount, we need to find the mount point by listing the mounts
	err := mnt.locateMountIP()
	if err != nil {
		log.Ctx(ctx).Error().Err(err).Msg("Failed to locate mount IP")
		return err
	}

	return mnt.decRef(ctx)
}
