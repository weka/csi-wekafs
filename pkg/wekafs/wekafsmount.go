package wekafs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"k8s.io/mount-utils"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

type wekafsMount struct {
	mounter       *wekafsMounter
	fsName        string
	mountPoint    string
	kMounter      mount.Interface
	mountOptions  MountOptions
	lastUsed      time.Time
	containerName string
}

func (m *wekafsMount) getMountPoint() string {
	if m.containerName != "" {
		return fmt.Sprintf("%s-%s", m.mountPoint, m.containerName)
	}
	return m.mountPoint
}

func (m *wekafsMount) getMountOptions() MountOptions {
	return m.mountOptions
}
func (m *wekafsMount) getLastUsed() time.Time {
	return m.lastUsed
}

func (m *wekafsMount) isMounted(ctx context.Context) bool {
	return anyMountIsMounted(ctx, m)
}

func (m *wekafsMount) getRefcountIdx() string {
	return m.getMountPoint() + "^" + m.getMountOptions().String()
}

func (m *wekafsMount) incRef(ctx context.Context, apiClient *apiclient.ApiClient) error {
	if m.mounter == nil {
		log.Ctx(ctx).Error().Msg("Mounter is nil")
		return errors.New("mounter is nil")
	}
	return anyMountIncRef(ctx, m, m.mounter.mountMap, apiClient)
}

func (m *wekafsMount) decRef(ctx context.Context) error {
	if m.mounter == nil {
		log.Ctx(ctx).Error().Msg("Mounter is nil")
		return errors.New("mounter is nil")
	}
	return anyMountDecRef(ctx, m, m.mounter.mountMap)
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
		return err
	}
	// WekaFS kernel module may return success from umount(2) while the mount remains
	// visible in /proc/mounts (e.g. when Bidirectional mount propagation holds a peer
	// reference on the host). Verify the mount is truly gone before considering this
	// a success, so decRef does not decrement refCount prematurely.
	if m.isMounted(ctx) {
		err := fmt.Errorf("mount point %s still exists in /proc/mounts after umount", m.getMountPoint())
		logger.Error().Err(err).Msg("Failed to unmount: mount still visible after umount returned success")
		return err
	}
	logger.Debug().Msg("Unmounted successfully")
	if err := os.Remove(m.getMountPoint()); err != nil {
		logger.Warn().Err(err).Msg("Failed to remove mount point directory, will be cleaned up on next use")
	} else {
		logger.Trace().Msg("Removed mount point successfully")
	}
	return nil
}

func (m *wekafsMount) ensureLocalContainerName(ctx context.Context, apiClient *apiclient.ApiClient) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.getMountPoint()).Str("filesystem", m.fsName).Logger()

	// already set
	if m.containerName != "" {
		return nil
	}

	var err error
	configuredContainerName := ""
	allowProtocolContainers := false
	// Read both straight off the driver config. Copying allowProtocolContainers down through the
	// mounter and the mount is what made --allowprotocolcontainers inert: the intermediate field
	// was never assigned, so the flag silently never reached EnsureLocalContainer.
	if m.mounter.config != nil {
		configuredContainerName = m.mounter.config.wekafsContainerName
		allowProtocolContainers = m.mounter.config.allowProtocolContainers
	}
	if m.containerName, err = apiClient.EnsureLocalContainer(ctx, allowProtocolContainers, configuredContainerName); err != nil {
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
	if !isWekaRunning(ctx) {
		logger.Error().Msg("WEKA is not running, cannot mount. Make sure WEKA client software is running on the host")
		return errors.New("weka is not running, cannot mount")
	}
	if apiClient == nil {
		return errors.New("no API client bound to the mount, cannot obtain a mount token")
	}
	mountToken, err := apiClient.GetMountTokenForFilesystemName(ctx, m.fsName)
	if err != nil {
		return err
	}
	mountOptionsSensitive = append(mountOptionsSensitive, fmt.Sprintf("token=%s", mountToken))

	// if needed, add containerName to the mount options
	if m.containerName != "" {
		mountOptions = mountOptions.AddOption(fmt.Sprintf("container_name=%s", m.containerName))
	}

	logger.Debug().Strs("mount_options", mountOptions.Strings()).Msg("Mounting wekafs filesystem")
	if err := m.kMounter.MountSensitive(m.fsName, m.getMountPoint(), "wekafs", mountOptions.Strings(), mountOptionsSensitive); err != nil {
		return err
	}
	logger.Debug().Msg("Mounted successfully")
	return nil
}
