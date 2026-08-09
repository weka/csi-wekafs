package wekafs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
	"k8s.io/mount-utils"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

type wekafsMount struct {
	mounter                 *wekafsMounter
	fsName                  string
	mountPoint              string
	kMounter                mount.Interface
	debugPath               string
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

func (m *wekafsMount) getMountOptions() MountOptions {
	return m.mountOptions
}
func (m *wekafsMount) getLastUsed() time.Time {
	return m.lastUsed
}

func (m *wekafsMount) isInDevMode() bool {
	return m.debugPath != ""
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

	// legacy flow
	if apiClient == nil {
		return nil
	}

	// dev mode, no actual wekafs mount happens
	if m.isInDevMode() {
		logger.Trace().Msg("In dev mode, skipping container name check")
		return nil
	}
	var err error
	configuredContainerName := ""
	if m.mounter.config != nil {
		configuredContainerName = m.mounter.config.wekafsContainerName
	}
	if m.containerName, err = apiClient.EnsureLocalContainer(ctx, m.allowProtocolContainers, configuredContainerName); err != nil {
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
	if !m.isInDevMode() {
		if !isWekaRunning(ctx) {
			logger.Error().Msg("WEKA is not running, cannot mount. Make sure WEKA client software is running on the host")
			return errors.New("weka is not running, cannot mount")
		}
		if apiClient == nil {
			// this flow is relevant only for legacy volumes, will not work with SCMC / authenticated mounts / non-root org
			logger.Trace().Msg("No API client for mount, not requesting mount token")
		} else {
			if mountToken, err := apiClient.GetMountTokenForFilesystemName(ctx, m.fsName); err != nil {
				return err
			} else {
				mountOptionsSensitive = append(mountOptionsSensitive, fmt.Sprintf("token=%s", mountToken))
			}
		}

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
	} else {
		fakePath := filepath.Join(m.debugPath, m.fsName)
		if err := os.MkdirAll(fakePath, DefaultVolumePermissions); err != nil {
			Die(fmt.Sprintf("Failed to create directory %s, while running in debug mode", fakePath))
		}
		logger.Trace().Strs("mount_options", m.getMountOptions().Strings()).Str("debug_path", m.debugPath).Msg("Performing mount")

		return m.kMounter.Mount(fakePath, m.getMountPoint(), "", []string{"bind"})
	}
}
