package wekafs

import (
	"context"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/mount-utils"
	"strings"
	"sync"
	"time"
)

const (
	inactiveMountGcPeriod = time.Minute * 10
)

type wekafsMounter struct {
	mountMap                wekafsMountsMap
	lock                    sync.Mutex
	kMounter                mount.Interface
	debugPath               string
	selinuxSupport          *bool
	gc                      *innerPathVolGc
	allowProtocolContainers bool
}

func (m *wekafsMounter) getGarbageCollector() *innerPathVolGc {
	return m.gc
}

func newWekafsMounter(driver *WekaFsDriver) *wekafsMounter {
	var selinuxSupport *bool
	if driver.selinuxSupport {
		log.Debug().Msg("SELinux support is forced")
		selinuxSupport = &[]bool{true}[0]
	}
	mounter := &wekafsMounter{mountMap: wekafsMountsMap{}, debugPath: driver.debugPath, selinuxSupport: selinuxSupport}
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
		debugPath:               m.debugPath,
		mountPoint:              "/run/weka-fs-mounts/" + getAsciiPart(fsName, 64) + "-" + uniqueId,
		mountOptions:            options,
		allowProtocolContainers: m.allowProtocolContainers,
	}
	return wMount
}

func (m *wekafsMounter) getSelinuxStatus(ctx context.Context) bool {
	if m.selinuxSupport != nil && *m.selinuxSupport {
		return true
	}
	selinuxSupport := getSelinuxStatus(ctx)
	m.selinuxSupport = &selinuxSupport
	return *m.selinuxSupport
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

func (m *wekafsMounter) LogActiveMounts() {
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

func (m *wekafsMounter) gcInactiveMounts() {
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

func (m *wekafsMounter) schedulePeriodicMountGc() {
	go func() {
		log.Debug().Msg("Initializing periodic mount GC for wekafs transport")
		for true {
			m.LogActiveMounts()
			m.gcInactiveMounts()
			time.Sleep(10 * time.Minute)
		}
	}()
}

func (m *wekafsMounter) getTransport() DataTransport {
	return dataTransportWekafs
}
