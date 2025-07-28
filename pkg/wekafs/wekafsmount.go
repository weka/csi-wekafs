package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"go.uber.org/atomic"
	"k8s.io/mount-utils"
	"os"
	"sync"
	"time"
)

type wekafsMount struct {
	mounter                 *wekafsMounter
	fsName                  string
	mountPoint              string
	refCount                int
	lock                    sync.Mutex
	kMounter                mount.Interface
	mountOptions            MountOptions
	lastUsed                time.Time
	allowProtocolContainers bool
	containerName           string
}

func (m *wekafsMount) getMountPoint() string {
	if m.containerName != "" {
		return fmt.Sprintf("%s-%s", m.mountPoint, m.containerName)
	}
	return m.mountPoint
}

func (m *wekafsMount) getRefCount() int {
	return m.refCount
}

func (m *wekafsMount) getMountOptions() MountOptions {
	return m.mountOptions
}
func (m *wekafsMount) getLastUsed() time.Time {
	return m.lastUsed
}

func (m *wekafsMount) isMounted() bool {
	return PathExists(m.getMountPoint()) && PathIsWekaMount(context.Background(), m.getMountPoint())
}

func (m *wekafsMount) getRefcountIdx() string {
	return m.getMountPoint() + "^" + m.getMountOptions().String()
}

func (m *wekafsMount) incRef(ctx context.Context, apiClient *apiclient.ApiClient) error {
	logger := log.Ctx(ctx)
	if m.mounter == nil {
		logger.Error().Msg("Mounter is nil")
		return errors.New("mounter is nil")
	}

	m.mounter.lock.Lock()
	defer m.mounter.lock.Unlock()
	refCount, ok := m.mounter.mountMap[m.getRefcountIdx()]

	if !ok || refCount == nil {
		refCount = atomic.NewInt32(0)
		m.mounter.mountMap[m.getRefcountIdx()] = refCount
	}

	if refCount.Load() > 0 && !m.isMounted() {
		logger.Warn().Str("mount_point", m.getMountPoint()).Int32("refcount", refCount.Load()).Msg("Mount not exists although should!")
	}

	if refCount.Load() == 0 {
		if err := m.doMount(ctx, apiClient, m.getMountOptions()); err != nil {
			return err
		}
	}
	refCount.Inc()
	logger.Trace().
		Int32("refcount", refCount.Load()).
		Strs("mount_options", m.getMountOptions().Strings()).
		Str("filesystem_name", m.fsName).
		Str("mount_point", m.getMountPoint()).
		Msg("RefCount increased")
	return nil
}

func (m *wekafsMount) decRef(ctx context.Context) error {
	logger := log.Ctx(ctx)
	if m.mounter == nil {
		logger.Error().Msg("Mounter is nil")
		return errors.New("mounter is nil")
	}
	m.mounter.lock.Lock()
	defer m.mounter.lock.Unlock()
	refCount, ok := m.mounter.mountMap[m.getRefcountIdx()]
	if !ok {
		logger.Error().Str("mount_options", m.getMountOptions().String()).Str("mount_point", m.getMountPoint()).Msg("During decRef refcount not found")
		refCount = atomic.NewInt32(0)
		m.mounter.mountMap[m.getRefcountIdx()] = refCount
	}

	if refCount.Load() < 0 {
		logger.Error().Int32("refcount", refCount.Load()).Msg("During decRef negative refcount encountered, probably due to failed unmount")
	}
	if refCount.Load() > 0 {
		refCount.Dec()
		logger.Trace().Int32("refcount", refCount.Load()).Strs("mount_options", m.getMountOptions().Strings()).Str("filesystem_name", m.fsName).Msg("RefCount decreased")
	}
	if refCount.Load() <= 0 {
		if m.isMounted() {
			if err := m.doUnmount(ctx); err != nil {
				return err
			}
			refCount.Store(0) // Reset refCount to 0 after unmount
		}
	}
	return nil
}

func (m *wekafsMount) locateContainerName() error {
	if m.containerName == "" {
		containerName, err := GetMountContainerNameFromActualMountPoint(m.mountPoint)
		if err != nil {
			return err
		}
		m.containerName = containerName
	}
	return nil
}

func (m *wekafsMount) doUnmount(ctx context.Context) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.getMountPoint()).Str("filesystem", m.fsName).Logger()
	logger.Trace().Strs("mount_options", m.getMountOptions().Strings()).Msg("Performing umount via k8s native mounter")
	err := m.kMounter.Unmount(m.getMountPoint())
	if err != nil {
		logger.Error().Err(err).Msg("Failed to unmount")
	} else {
		logger.Trace().Msg("Unmounted successfully")
		if err := os.Remove(m.getMountPoint()); err != nil {
			logger.Error().Err(err).Msg("Failed to remove mount point")
			return err
		} else {
			logger.Trace().Msg("Removed mount point successfully")
		}
	}
	return err
}

func (m *wekafsMount) ensureLocalContainerName(ctx context.Context, apiClient *apiclient.ApiClient) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.getMountPoint()).Str("filesystem", m.fsName).Logger()

	// already set
	if m.containerName != "" {
		return nil
	}

	var err error
	if m.containerName, err = apiClient.EnsureLocalContainer(ctx, m.allowProtocolContainers); err != nil {
		logger.Error().Err(err).Msg("Failed to ensure local container")
	}
	return nil
}

func (m *wekafsMount) doMount(ctx context.Context, apiClient *apiclient.ApiClient, mountOptions MountOptions) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.getMountPoint()).Str("filesystem", m.fsName).Logger()
	var mountOptionsSensitive []string
	if err := os.MkdirAll(m.getMountPoint(), DefaultVolumePermissions); err != nil {
		return err
	}
	if !isWekaRunning() {
		logger.Error().Msg("WEKA is not running, cannot mount. Make sure WEKA client software is running on the host")
		return errors.New("weka is not running, cannot mount")
	}
	if apiClient == nil {
		return errors.New("no api client bound, cannot obtain mount token")
	}

	if mountToken, err := apiClient.GetMountTokenForFilesystemName(ctx, m.fsName); err != nil {
		return err
	} else {
		mountOptionsSensitive = append(mountOptionsSensitive, fmt.Sprintf("token=%s", mountToken))
	}

	// if needed, add containerName to the mount options
	if m.containerName != "" {
		mountOptions = mountOptions.AddOption(fmt.Sprintf("container_name=%s", m.containerName))
	}

	logger.Trace().Strs("mount_options", mountOptions.Strings()).Msg("Performing mount")
	return m.kMounter.MountSensitive(m.fsName, m.getMountPoint(), "wekafs", mountOptions.Strings(), mountOptionsSensitive)
}
