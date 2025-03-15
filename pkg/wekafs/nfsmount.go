package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/mount-utils"
	"os"
	"strings"
	"time"
)

type nfsMount struct {
	mounter         *nfsMounter
	fsName          string
	mountPoint      string
	kMounter        mount.Interface
	mountOptions    MountOptions
	lastUsed        time.Time
	mountIpAddress  string
	clientGroupName string
	protocolVersion apiclient.NfsVersionString
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

func (m *nfsMount) isMounted() bool {
	return PathExists(m.getMountPoint()) && PathIsWekaMount(context.Background(), m.getMountPoint())
}

func (m *nfsMount) getRefcountIdx() string {
	return m.getMountPoint() + "^" + m.getMountOptions().AsNfs().String()
}

func (m *nfsMount) incRef(ctx context.Context, apiClient *apiclient.ApiClient) error {
	logger := log.Ctx(ctx)
	if m.mounter == nil {
		logger.Error().Msg("Mounter is nil")
		return errors.New("mounter is nil")
	}

	m.mounter.lock.Lock()
	defer m.mounter.lock.Unlock()
	refCount, ok := m.mounter.mountMap[m.getRefcountIdx()]
	if !ok {
		refCount = 0
	}
	if refCount == 0 {
		if err := m.doMount(ctx, apiClient, m.getMountOptions()); err != nil {
			return err
		}
	}
	if refCount > 0 && !m.isMounted() {
		logger.Warn().Str("mount_point", m.getMountPoint()).Int("refcount", refCount).Msg("Mount not exists although should!")
		if err := m.doMount(ctx, apiClient, m.getMountOptions()); err != nil {
			return err
		}
	}
	refCount++
	m.mounter.mountMap[m.getRefcountIdx()] = refCount

	logger.Trace().
		Int("refcount", refCount).
		Strs("mount_options", m.getMountOptions().Strings()).
		Str("filesystem_name", m.fsName).
		Str("mount_point", m.getMountPoint()).
		Msg("RefCount increased")
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
	refCount, ok := m.mounter.mountMap[m.getRefcountIdx()]
	if !ok {
		logger.Error().Int("refcount", refCount).Str("mount_options", m.getMountOptions().String()).Str("mount_point", m.getMountPoint()).Msg("During decRef refcount not found")
		refCount = 0
	}
	if refCount < 0 {
		logger.Error().Int("refcount", refCount).Msg("During decRef negative refcount encountered, probably due to failed unmount")
	}
	if refCount > 0 {
		logger.Trace().Int("refcount", refCount).Strs("mount_options", m.getMountOptions().Strings()).Str("filesystem_name", m.fsName).Msg("RefCount decreased")
		refCount--
		m.mounter.mountMap[m.getRefcountIdx()] = refCount
	}
	if refCount == 0 {
		if m.isMounted() {
			if err := m.doUnmount(ctx); err != nil {
				return err
			}
		}
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
		if err := os.Remove(m.getMountPoint()); err != nil {
			logger.Error().Err(err).Msg("Failed to remove mount point")
			return err
		} else {
			logger.Trace().Msg("Removed mount point successfully")
		}
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
	logger := log.Ctx(ctx).With().Str("mount_point", m.getMountPoint()).Str("filesystem", m.fsName).Logger()
	var mountOptionsSensitive []string
	if apiClient == nil {
		// this flow is relevant only for legacy volumes, will not work with SCMC
		logger.Trace().Msg("No API client for mount, cannot proceed")
		return errors.New("no API client for mount, cannot do NFS mount")
	}
	// to validate that the organization is root, otherwise cannot mount NFS volumes
	if apiClient.ApiOrgId != 0 {
		err := errors.New("cannot mount NFS volumes with non-Root organization")
		logger.Error().Err(err).Int("organization_id", apiClient.ApiOrgId).Msg("Cannot mount NFS volumes with non-Root organization")
		return err
	}

	err := apiClient.EnsureNfsPermissions(ctx, m.fsName, apiclient.NfsVersionV4, m.clientGroupName)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to ensure NFS permissions")
		return errors.New("failed to ensure NFS permissions")
	}

	mountTarget := m.mountIpAddress + ":/" + m.fsName
	logger.Trace().
		Strs("mount_options", m.getMountOptions().Strings()).
		Str("mount_target", mountTarget).
		Str("mount_point", m.getMountPoint()).
		Str("mount_ip_address", m.mountIpAddress).
		Msg("Performing mount")

	logger.Trace().Msg("Ensuring mount point exists")
	if err := os.MkdirAll(m.getMountPoint(), DefaultVolumePermissions); err != nil {
		return err
	}
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		err = m.kMounter.MountSensitive(mountTarget, m.getMountPoint(), "nfs", mountOptions.Strings(), mountOptionsSensitive)
		if err == nil {
			logger.Trace().Msg("Mounted successfully")
			return nil
		}
		if os.IsNotExist(err) || strings.Contains(strings.ToLower(err.Error()), "no such file or directory") {
			logger.Error().Err(err).Msg("Mount point not found")
		} else if os.IsPermission(err) {
			logger.Error().Err(err).Msg("Mount failed due to permissions issue")
		} else if strings.Contains(err.Error(), "invalid argument") {
			logger.Error().Err(err).Msg("Mount failed due to invalid argument")
		} else {
			logger.Error().Err(err).Msg("Mount failed due to unknown issue")
		}
		logger.Warn().Int("attempt", i+1).Msg("Retrying mount")
		time.Sleep(2 * time.Second) // Optional: Add a delay between retries
	}
	logger.Error().Err(err).Int("retry_count", maxRetries).Msg("Failed to mount after retries")
	return err
}
