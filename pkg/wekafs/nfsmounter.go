package wekafs

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"k8s.io/mount-utils"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

type nfsMounter struct {
	mountMap              *mountMap
	kMounter              mount.Interface
	gc                    *innerPathVolGc
	clientGroupName       string
	nfsProtocolVersion    string
	exclusiveMountOptions []mutuallyExclusiveMountOptionSet
	mountBaseDir          string
	mounterState
}

func (m *nfsMounter) getGarbageCollector() *innerPathVolGc {
	return m.gc
}

func newNfsMounter(ctx context.Context, driver *WekaFsDriver) *nfsMounter {
	mounter := &nfsMounter{mountMap: newMountMap(), exclusiveMountOptions: driver.config.mutuallyExclusiveOptions, mountBaseDir: mountBaseDirForRole(driver.csiMode, dataTransportNfs)}
	if driver.selinuxSupport {
		log.Ctx(ctx).Debug().Msg("SELinux support is forced")
		mounter.forceSelinux()
	}
	mounter.gc = initInnerPathVolumeGc(mounter)
	mounter.gc.config = driver.config
	mounter.schedulePeriodicMountGc(ctx)
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
		mountPoint:      m.mountBaseDir + "/" + getAsciiPart(fsName, 64) + "-" + uniqueId,
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

func (m *nfsMounter) LogActiveMounts(ctx context.Context) {
	anyMounterLogActiveMounts(ctx, m.mountMap, m.getTransport())
}

func (m *nfsMounter) gcInactiveMounts(ctx context.Context) {
	anyMounterGcInactiveMounts(ctx, m.mountMap)
}

func (m *nfsMounter) schedulePeriodicMountGc(ctx context.Context) {
	anyMounterSchedulePeriodicMountGc(ctx, m)
}

func (m *nfsMounter) getTransport() DataTransport {
	return dataTransportNfs
}
