package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/mount-utils"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type nfsMount struct {
	fsName         string
	mountPoint     string
	refCount       int
	lock           sync.Mutex
	kMounter       mount.Interface
	debugPath      string
	mountOptions   MountOptions
	lastUsed       time.Time
	mountIpAddress string
}

func (m *nfsMount) getMountPoint() string {
	return m.mountPoint
}

func (m *nfsMount) getRefCount() int {
	return m.refCount
}

func (m *nfsMount) getMountOptions() MountOptions {
	return m.mountOptions
}

func (m *nfsMount) getLastUsed() time.Time {
	return m.lastUsed
}

func (m *nfsMount) isInDevMode() bool {
	return m.debugPath != ""
}

func (m *nfsMount) isMounted() bool {
	return PathExists(m.mountPoint) && PathIsWekaMount(context.Background(), m.mountPoint)
}

func (m *nfsMount) incRef(ctx context.Context, apiClient *apiclient.ApiClient) error {
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

func (m *nfsMount) decRef(ctx context.Context) error {
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

func (m *nfsMount) doUnmount(ctx context.Context) error {
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

func (m *nfsMount) ensureMountIpAddress(ctx context.Context, apiClient *apiclient.ApiClient) error {
	if m.mountIpAddress == "" {
		ip, err := apiClient.GetNfsMountIp(ctx)
		if err != nil {
			return err
		}
		m.mountIpAddress = ip
	}
	return nil
}

func (m *nfsMount) doMount(ctx context.Context, apiClient *apiclient.ApiClient, mountOptions MountOptions) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.mountPoint).Str("filesystem", m.fsName).Logger()
	var mountOptionsSensitive []string
	if err := os.MkdirAll(m.mountPoint, DefaultVolumePermissions); err != nil {
		return err
	}
	if !m.isInDevMode() {
		if apiClient == nil {
			// this flow is relevant only for legacy volumes, will not work with SCMC
			logger.Trace().Msg("No API client for mount, cannot proceed")
			return errors.New("no API client for mount, cannot do NFS mount")
		}

		nodeIP := apiclient.GetNodeIpAddress()
		if apiClient.EnsureNfsPermissions(ctx, nodeIP, m.fsName) != nil {
			logger.Error().Msg("Failed to ensure NFS permissions")
			return errors.New("failed to ensure NFS permissions")
		}

		if err := m.ensureMountIpAddress(ctx, apiClient); err != nil {
			logger.Error().Err(err).Msg("Failed to get mount IP address")
			return err
		}

		mountTarget := m.mountIpAddress + ":/" + m.fsName
		logger.Trace().
			Strs("mount_options", m.mountOptions.Strings()).
			Str("mount_target", mountTarget).
			Fields(mountOptions).
			Msg("Performing mount")
		return m.kMounter.MountSensitive(mountTarget, m.mountPoint, "nfs", mountOptions.Strings(), mountOptionsSensitive)
	} else {
		fakePath := filepath.Join(m.debugPath, m.fsName)
		if err := os.MkdirAll(fakePath, DefaultVolumePermissions); err != nil {
			Die(fmt.Sprintf("Failed to create directory %s, while running in debug mode", fakePath))
		}
		logger.Trace().Strs("mount_options", m.mountOptions.Strings()).Str("debug_path", m.debugPath).Msg("Performing mount")

		return m.kMounter.Mount(fakePath, m.mountPoint, "", []string{"bind"})
	}
}
