package wekafs

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/mount-utils"
	"strings"
	"sync"
	"time"
)

type nfsMounter struct {
	mountMap              nfsMountsMap
	lock                  sync.Mutex
	kMounter              mount.Interface
	debugPath             string
	selinuxSupport        *bool
	gc                    *innerPathVolGc
	interfaceGroupName    string
	clientGroupName       string
	nfsProtocolVersion    string
	exclusiveMountOptions []mutuallyExclusiveMountOptionSet
}

func (m *nfsMounter) getGarbageCollector() *innerPathVolGc {
	return m.gc
}

func newNfsMounter(driver *WekaFsDriver) *nfsMounter {
	var selinuxSupport *bool
	if driver.selinuxSupport {
		log.Debug().Msg("SELinux support is forced")
		selinuxSupport = &[]bool{true}[0]
	}
	mounter := &nfsMounter{mountMap: make(nfsMountsMap), debugPath: driver.debugPath, selinuxSupport: selinuxSupport, exclusiveMountOptions: driver.config.mutuallyExclusiveOptions}
	mounter.gc = initInnerPathVolumeGc(mounter)
	mounter.schedulePeriodicMountGc()
	mounter.interfaceGroupName = driver.config.interfaceGroupName
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
		mounter:            m,
		kMounter:           m.kMounter,
		fsName:             fsName,
		debugPath:          m.debugPath,
		mountPoint:         "/run/weka-fs-mounts/" + getAsciiPart(fsName, 64) + "-" + uniqueId,
		mountOptions:       options,
		interfaceGroupName: m.interfaceGroupName,
		clientGroupName:    m.clientGroupName,
		protocolVersion:    apiclient.NfsVersionString(fmt.Sprintf("V%s", m.nfsProtocolVersion)),
	}
	return wMount
}

func (m *nfsMounter) getSelinuxStatus(ctx context.Context) bool {
	if m.selinuxSupport != nil && *m.selinuxSupport {
		return true
	}
	selinuxSupport := getSelinuxStatus(ctx)
	m.selinuxSupport = &selinuxSupport
	return *m.selinuxSupport
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

func (m *nfsMounter) LogActiveMounts() {
	m.lock.Lock()
	defer m.lock.Unlock()
	if len(m.mountMap) > 0 {
		count := 0
		for refIndex := range m.mountMap {
			if mapEntry, ok := m.mountMap[refIndex]; ok {
				parts := strings.Split(refIndex, "^")
				logger := log.With().Str("mount_point", parts[0]).Str("mount_options", parts[1]).Str("ref_index", refIndex).Int("refcount", mapEntry).Logger()

				if mapEntry > 0 {
					logger.Trace().Msg("Mount is active")
					count++
				} else {
					logger.Trace().Msg("Mount is not active")
				}

			}
		}
		log.Debug().Int("total", len(m.mountMap)).Int("active", count).Msg("Periodic checkup on mount map")
	}
}

func (m *nfsMounter) gcInactiveMounts() {
	m.lock.Lock()
	defer m.lock.Unlock()
	if len(m.mountMap) > 0 {
		for refIndex := range m.mountMap {
			if mapEntry, ok := m.mountMap[refIndex]; ok {
				if mapEntry == 0 {
					parts := strings.Split(refIndex, "^")
					logger := log.With().Str("mount_point", parts[0]).Str("mount_options", parts[1]).Str("ref_index", refIndex).Logger()
					logger.Trace().Msg("Removing inactive mount from map")
					delete(m.mountMap, refIndex)
				}
			}
		}
	}
}

func (m *nfsMounter) schedulePeriodicMountGc() {
	go func() {
		log.Debug().Msg("Initializing periodic mount GC")
		for true {
			m.LogActiveMounts()
			m.gcInactiveMounts()
			time.Sleep(10 * time.Minute)
		}
	}()
}

func (m *nfsMounter) getTransport() DataTransport {
	return dataTransportNfs
}
