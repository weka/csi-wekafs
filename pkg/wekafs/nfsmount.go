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
	"strings"
	"sync"
	"time"
)

type nfsMount struct {
	fsName             string
	mountPoint         string
	refCount           int
	lock               sync.Mutex
	kMounter           mount.Interface
	debugPath          string
	mountOptions       MountOptions
	lastUsed           time.Time
	mountIpAddress     string
	interfaceGroupName *string
	clientGroupName    string
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
		ip, err := apiClient.GetNfsMountIp(ctx, m.interfaceGroupName)
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

		if err := m.ensureMountIpAddress(ctx, apiClient); err != nil {
			logger.Error().Err(err).Msg("Failed to get mount IP address")
			return err
		}

		nodeIP, err := apiclient.GetNodeIpAddressByRouting(m.mountIpAddress)
		if err != nil {
			logger.Error().Err(err).Msg("Failed to get routed node IP address, relying on node IP")
			nodeIP = apiclient.GetNodeIpAddress()
		}

		if apiClient.EnsureNfsPermissions(ctx, nodeIP, m.fsName, m.clientGroupName) != nil {
			logger.Error().Msg("Failed to ensure NFS permissions")
			return errors.New("failed to ensure NFS permissions")
		}

		mountTarget := m.mountIpAddress + ":/" + m.fsName
		logger.Trace().
			Strs("mount_options", m.mountOptions.Strings()).
			Str("mount_target", mountTarget).
			Str("mount_ip_address", m.mountIpAddress).
			Msg("Performing mount")

		err = m.kMounter.MountSensitive(mountTarget, m.mountPoint, "nfs", mountOptions.Strings(), mountOptionsSensitive)
		if err != nil {
			if os.IsNotExist(err) {
				logger.Error().Err(err).Msg("Mount target not found")
			} else if os.IsPermission(err) {
				logger.Error().Err(err).Msg("Mount failed due to permissions issue")
				return err
			} else if strings.Contains(err.Error(), "invalid argument") {
				logger.Error().Err(err).Msg("Mount failed due to invalid argument")
				return err
			} else {
				logger.Error().Err(err).Msg("Mount failed due to unknown issue")
			}
			return err
		}
		logger.Trace().Msg("Mounted successfully")
		return nil
	} else {
		fakePath := filepath.Join(m.debugPath, m.fsName)
		if err := os.MkdirAll(fakePath, DefaultVolumePermissions); err != nil {
			Die(fmt.Sprintf("Failed to create directory %s, while running in debug mode", fakePath))
		}
		logger.Trace().Strs("mount_options", m.mountOptions.Strings()).Str("debug_path", m.debugPath).Msg("Performing mount")

		return m.kMounter.Mount(fakePath, m.mountPoint, "", []string{"bind"})
	}
}
