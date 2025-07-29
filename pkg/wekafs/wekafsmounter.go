package wekafs

import (
	"context"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/mount-utils"
)

type wekafsMounter struct {
	mountMap                wekafsMountsMap
	lock                    sync.Mutex
	kMounter                mount.Interface
	selinuxSupport          *bool
	gc                      *innerPathVolGc
	allowProtocolContainers bool
	config                  *DriverConfig
	mountBaseDir            string
	enabled                 bool
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

func mountBaseDirForRole(mode CsiPluginMode) string {
	switch mode {
	case CsiModeNode:
		return "/run/weka-fs-mounts-node"
	case CsiModeController:
		return "/run/weka-fs-mounts-controller"
	default:
		return "/run/weka-fs-mounts"
	}
}

func (m *wekafsMounter) getGarbageCollector() *innerPathVolGc {
	return m.gc
}

func newWekafsMounter(ctx context.Context, driver *WekaFsDriver) *wekafsMounter {
	var selinuxSupport *bool
	if driver.selinuxSupport {
		log.Debug().Msg("SELinux support is forced")
		selinuxSupport = &[]bool{true}[0]
	}
	mounter := &wekafsMounter{
		mountMap: wekafsMountsMap{},
		selinuxSupport: selinuxSupport,
		config: driver.config,
		mountBaseDir: mountBaseDirForRole(driver.csiMode),
		enabled: true,
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
		mounter:                 m,
		kMounter:                m.kMounter,
		fsName:                  fsName,
		mountPoint:              path.Join(m.mountBaseDir, string(m.getTransport()), getAsciiPart(fsName, 64)+"-"+uniqueId),
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
	m.lock.Lock()
	defer m.lock.Unlock()
	if len(m.mountMap) > 0 {
		active := 0
		for refIndex := range m.mountMap {
			if mapEntry, ok := m.mountMap[refIndex]; ok {
				parts := strings.Split(refIndex, "^")
				mountPoint := parts[0]
				actuallyMounted := PathIsWekaMount(ctx, mountPoint)
				logger := log.With().Str("mount_point", mountPoint).Str("mount_options", parts[1]).Int("refcount", mapEntry).Bool("in_proc_mounts", actuallyMounted).Logger()

				if mapEntry > 0 {
					active++
					if !actuallyMounted {
						logger.Warn().Msg("Mount has positive refcount but is not in /proc/mounts")
					} else {
						logger.Trace().Msg("Mount is active")
					}
				} else {
					if actuallyMounted {
						logger.Warn().Msg("Mount has zero refcount but is still in /proc/mounts")
					} else {
						logger.Trace().Msg("Mount is inactive")
					}
				}
			}
		}
		log.Debug().Int("total", len(m.mountMap)).Int("active", active).Msg("Periodic checkup on mount map")
	}
}

func (m *wekafsMounter) gcInactiveMounts(ctx context.Context) {
	m.lock.Lock()
	defer m.lock.Unlock()
	if len(m.mountMap) > 0 {
		for refIndex := range m.mountMap {
			if mapEntry, ok := m.mountMap[refIndex]; ok {
				if mapEntry == 0 {
					parts := strings.Split(refIndex, "^")
					mountPoint := parts[0]
					logger := log.With().Str("mount_point", mountPoint).Str("mount_options", parts[1]).Logger()
					if PathIsWekaMount(ctx, mountPoint) {
						logger.Warn().Msg("Removing stale mount map entry, but mount is still in /proc/mounts — possible mount leak")
					} else {
						logger.Trace().Msg("Removing inactive mount from map")
					}
					delete(m.mountMap, refIndex)
				}
			}
		}
	}
}

func (m *wekafsMounter) schedulePeriodicMountGc(ctx context.Context) {
	go func() {
		log.Debug().Msg("Initializing periodic mount GC for wekafs transport")
		for {
			m.LogActiveMounts(ctx)
			m.gcInactiveMounts(ctx)
			select {
			case <-ctx.Done():
				log.Debug().Msg("Stopping periodic mount GC for wekafs transport")
				return
			case <-time.After(inactiveMountGcPeriod):
			}
		}
	}()
}

func (m *wekafsMounter) getTransport() DataTransport {
	return dataTransportWekafs
}
