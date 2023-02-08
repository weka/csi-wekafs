package wekafs

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/utils/mount"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type wekaMount struct {
	fsName       string
	mountPoint   string
	refCount     int
	lock         sync.Mutex
	kMounter     mount.Interface
	debugPath    string
	mountOptions MountOptions
	lastUsed     time.Time
}

func (m *wekaMount) isInDebugMode() bool {
	return m.debugPath != ""
}

func (m *wekaMount) isMounted() bool {
	return PathExists(m.mountPoint) && PathIsWekaMount(context.Background(), m.mountPoint)
}

func (m *wekaMount) incRef(ctx context.Context, apiClient *apiclient.ApiClient) error {
	logger := log.Ctx(ctx)
	m.lock.Lock()
	defer m.lock.Unlock()
	if m.refCount < 0 {
		logger.Error().Str("mount_point", m.mountPoint).Int("refcount", m.refCount).Msg("During incRef negative refcount encountered")
		m.refCount = 0 // to make sure that we don't have negative refcount later
	}
	if m.refCount == 0 {
		if err := m.doMount(ctx, apiClient, m.mountOptions); err != nil {
			return err
		}
	} else if !m.isMounted() {
		logger.Warn().Str("mount_point", m.mountPoint).Int("refcount", m.refCount).Msg("Mount not exists although should!")
		if err := m.doMount(ctx, apiClient, m.mountOptions); err != nil {
			return err
		}

	}
	m.refCount++
	logger.Trace().Int("refcount", m.refCount).Strs("mount_options", m.mountOptions.Strings()).Str("filesystem_name", m.fsName).Msg("RefCount increased")
	return nil
}

func (m *wekaMount) decRef(ctx context.Context) error {
	logger := log.Ctx(ctx)
	m.lock.Lock()
	defer m.lock.Unlock()
	m.refCount--
	m.lastUsed = time.Now()
	logger.Trace().Int("refcount", m.refCount).Strs("mount_options", m.mountOptions.Strings()).Str("filesystem_name", m.fsName).Msg("RefCount decreased")
	if m.refCount < 0 {
		logger.Error().Int("refcount", m.refCount).Msg("During decRef negative refcount encountered")
		m.refCount = 0 // to make sure that we don't have negative refcount later
	}
	if m.refCount == 0 {
		if err := m.doUnmount(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (m *wekaMount) doUnmount(ctx context.Context) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.mountPoint).Str("filesystem", m.fsName).Logger()
	logger.Trace().Strs("mount_options", m.mountOptions.Strings()).Msg("Performing umount via k8s native mounter")
	err := m.kMounter.Unmount(m.mountPoint)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to unmount")
	} else {
		logger.Trace().Msg("Unmounted successfully")
	}
	return err
}

func (m *wekaMount) doMount(ctx context.Context, apiClient *apiclient.ApiClient, mountOptions MountOptions) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.mountPoint).Str("filesystem", m.fsName).Logger()
	mountToken := ""
	var mountOptionsSensitive []string
	if err := os.MkdirAll(m.mountPoint, DefaultVolumePermissions); err != nil {
		return err
	}
	if !m.isInDebugMode() {
		if apiClient == nil {
			logger.Trace().Msg("No API client for mount, not requesting mount token")
		} else {
			var err error
			if mountToken, err = apiClient.GetMountTokenForFilesystemName(ctx, m.fsName); err != nil {
				return err
			}
			mountOptionsSensitive = append(mountOptionsSensitive, fmt.Sprintf("token=%s", mountToken))
		}
		logger.Trace().Strs("mount_options", m.mountOptions.Strings()).
			Fields(mountOptions).Msg("Performing mount")
		return m.kMounter.MountSensitive(m.fsName, m.mountPoint, "wekafs", mountOptions.Strings(), mountOptionsSensitive)
	} else {
		fakePath := filepath.Join(m.debugPath, m.fsName)
		if err := os.MkdirAll(fakePath, DefaultVolumePermissions); err != nil {
			Die(fmt.Sprintf("Failed to create directory %s, while running in debug mode", fakePath))
		}
		logger.Trace().Strs("mount_options", m.mountOptions.Strings()).Str("debug_path", m.debugPath).Msg("Performing mount")

		return m.kMounter.Mount(fakePath, m.mountPoint, "", []string{"bind"})
	}
}
