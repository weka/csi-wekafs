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
	"time"
)

type nfsMount struct {
	mounter            *nfsMounter
	fsName             string
	mountPoint         string
	kMounter           mount.Interface
	debugPath          string
	mountOptions       MountOptions
	lastUsed           time.Time
	mountIpAddress     string
	interfaceGroupName string
	clientGroupName    string
	protocolVersion    apiclient.NfsVersionString
}

func (m *nfsMount) getMountPoint() string {
	return fmt.Sprintf("%s-%s", m.mountPoint, m.mountIpAddress)
}

func (m *nfsMount) getRefCount() int {
	return 0
}

func (m *nfsMount) getMountOptions() MountOptions {
	return m.mountOptions.AddOption(fmt.Sprintf("vers=%s", m.protocolVersion.AsOption()))
}

func (m *nfsMount) getLastUsed() time.Time {
	return m.lastUsed
}

func (m *nfsMount) isInDevMode() bool {
	return m.debugPath != ""
}

func (m *nfsMount) isMounted() bool {
	return PathExists(m.getMountPoint()) && PathIsWekaMount(context.Background(), m.mountPoint)
}

func (m *nfsMount) incRef(ctx context.Context, apiClient *apiclient.ApiClient) error {
	logger := log.Ctx(ctx)
	if m.mounter == nil {
		logger.Error().Msg("Mounter is nil")
		return errors.New("mounter is nil")
	}
	m.mounter.lock.Lock()
	defer m.mounter.lock.Unlock()
	refCount, ok := m.mounter.mountMap[m.getMountPoint()]
	if !ok {
		refCount = 0
	}
	if refCount == 0 {
		if err := m.doMount(ctx, apiClient, m.getMountOptions()); err != nil {
			return err
		}
	} else if !m.isMounted() {
		logger.Warn().Str("mount_point", m.getMountPoint()).Int("refcount", refCount).Msg("Mount not exists although should!")
		if err := m.doMount(ctx, apiClient, m.getMountOptions()); err != nil {
			return err
		}

	}
	refCount++
	m.mounter.mountMap[m.getMountPoint()] = refCount

	logger.Trace().Int("refcount", refCount).Strs("mount_options", m.getMountOptions().Strings()).Str("filesystem_name", m.fsName).Msg("RefCount increased")
	return nil
}

func (m *nfsMount) decRef(ctx context.Context) error {
	logger := log.Ctx(ctx)
	if m.mounter == nil {
		logger.Error().Msg("Mounter is nil")
		return errors.New("mounter is nil")
	}
	m.mounter.lock.Lock()
	defer m.mounter.lock.Unlock()
	refCount, ok := m.mounter.mountMap[m.getMountPoint()]
	defer func() {
		if refCount == 0 {
			delete(m.mounter.mountMap, m.getMountPoint())
		} else {
			m.mounter.mountMap[m.getMountPoint()] = refCount
		}
	}()
	if !ok {
		refCount = 0
	}
	if refCount < 0 {
		logger.Error().Int("refcount", refCount).Msg("During decRef negative refcount encountered")
		refCount = 0 // to make sure that we don't have negative refcount later
	}
	if refCount == 1 {
		if err := m.doUnmount(ctx); err != nil {
			return err
		}
		refCount--
	}
	return nil
}

func (m *nfsMount) locateMountIP() error {
	if m.mountIpAddress == "" {
		ipAddr, err := GetMountIpFromActualMountPoint(m.mountPoint)
		if err != nil {
			return err
		}
		m.mountIpAddress = ipAddr
	}
	return nil
}

func (m *nfsMount) doUnmount(ctx context.Context) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.getMountPoint()).Str("filesystem", m.fsName).Logger()
	logger.Trace().Strs("mount_options", m.getMountOptions().Strings()).Msg("Performing umount via k8s native mounter")
	err := m.kMounter.Unmount(m.getMountPoint())
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
	logger := log.Ctx(ctx).With().Str("mount_point", m.getMountPoint()).Str("filesystem", m.fsName).Logger()
	var mountOptionsSensitive []string
	if apiClient == nil {
		// this flow is relevant only for legacy volumes, will not work with SCMC
		logger.Trace().Msg("No API client for mount, cannot proceed")
		return errors.New("no API client for mount, cannot do NFS mount")
	}

	if err := m.ensureMountIpAddress(ctx, apiClient); err != nil {
		logger.Error().Err(err).Msg("Failed to get mount IP address")
		return err
	}

	if err := os.MkdirAll(m.getMountPoint(), DefaultVolumePermissions); err != nil {
		return err
	}
	if !m.isInDevMode() {

		nodeIP, err := apiclient.GetNodeIpAddressByRouting(m.mountIpAddress)
		if err != nil {
			logger.Error().Err(err).Msg("Failed to get routed node IP address, relying on node IP")
			nodeIP = apiclient.GetNodeIpAddress()
		}

		if apiClient.EnsureNfsPermissions(ctx, nodeIP, m.fsName, apiclient.NfsVersionV4, m.clientGroupName) != nil {
			logger.Error().Msg("Failed to ensure NFS permissions")
			return errors.New("failed to ensure NFS permissions")
		}

		mountTarget := m.mountIpAddress + ":/" + m.fsName
		logger.Trace().
			Strs("mount_options", m.getMountOptions().Strings()).
			Str("mount_target", mountTarget).
			Str("mount_ip_address", m.mountIpAddress).
			Msg("Performing mount")

		err = m.kMounter.MountSensitive(mountTarget, m.getMountPoint(), "nfs", mountOptions.Strings(), mountOptionsSensitive)
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
		logger.Trace().Strs("mount_options", m.getMountOptions().Strings()).Str("debug_path", m.debugPath).Msg("Performing mount")

		return m.kMounter.Mount(fakePath, m.getMountPoint(), "", []string{"bind"})
	}
}
