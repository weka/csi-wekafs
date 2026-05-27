package wekafs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/mount-utils"
)

type wekafsMount struct {
	mounter                 *wekafsMounter
	fsName                  string
	mountPoint              string
	kMounter                mount.Interface
	mountOptions            MountOptions
	lastUsed                time.Time
	allowProtocolContainers bool
	containerName           string
}

func (m *wekafsMount) getKMounter() mount.Interface {
	return m.kMounter
}

func (m *wekafsMount) getFsName() string {
	return m.fsName
}

func (m *wekafsMount) getMounter() AnyMounter {
	return m.mounter
}

func (m *wekafsMount) getMountPoint() string {
	return fmt.Sprintf("%s-%s", m.mountPoint, m.containerName)
}

func (m *wekafsMount) getMountOptions() MountOptions {
	return m.mountOptions.AddOption(fmt.Sprintf("container_name=%s", m.containerName))
}

func (m *wekafsMount) getLastUsed() time.Time {
	return m.lastUsed
}

func (m *wekafsMount) isMounted(ctx context.Context) bool {
	return anyMountIsMounted(ctx, m)
}

func (m *wekafsMount) getRefCountIndex() string {
	return anyMountGetRefCountIndex(m)
}

func (m *wekafsMount) incRef(ctx context.Context, apiClient *apiclient.ApiClient) error {
	return anyMountIncref(ctx, apiClient, m)
}

func (m *wekafsMount) decRef(ctx context.Context) error {
	return anyMountDecRef(ctx, m)
}

func (m *wekafsMount) doUnmount(ctx context.Context) error {
	return anyMountDoUnmount(ctx, m)
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

func (m *wekafsMount) ensureLocalContainerName(ctx context.Context, apiClient *apiclient.ApiClient) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.getMountPoint()).Str("filesystem", m.fsName).Logger()

	// already set
	if m.containerName != "" {
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
	m.mountOptions.AddOption(fmt.Sprintf("container_name=%s", m.containerName))
	return nil
}

func (m *wekafsMount) doMount(ctx context.Context, apiClient *apiclient.ApiClient, mountOptions MountOptions) error {
	logger := log.Ctx(ctx).With().
		Str("mount_point", m.getMountPoint()).
		Str("filesystem", m.fsName).
		Strs("mount_options", mountOptions.Strings()).
		Logger()
	var mountOptionsSensitive []string
	if apiClient == nil {
		logger.Trace().Msg("No API client for mount, cannot proceed")
		return errors.New("no api client bound, cannot obtain mount token")
	}

	logger.Trace().Msg("Ensuring mount point exists")
	if err := os.MkdirAll(m.getMountPoint(), DefaultVolumePermissions); err != nil {
		return err
	}
	if !isWekaRunning(ctx) {
		logger.Error().Msg("WEKA is not running, cannot mount. Make sure WEKA client software is running on the host")
		return errors.New("weka is not running, cannot mount")
	}

	if mountToken, err := apiClient.GetMountTokenForFilesystemName(ctx, m.fsName); err != nil {
		return err
	} else {
		mountOptionsSensitive = append(mountOptionsSensitive, fmt.Sprintf("token=%s", mountToken))
	}

	logger.Debug().Strs("mount_options", mountOptions.Strings()).Msg("Mounting wekafs filesystem")
	err := m.kMounter.MountSensitive(m.fsName, m.getMountPoint(), "wekafs", mountOptions.Strings(), mountOptionsSensitive)
	if err == nil {
		logger.Debug().Msg("Mounted successfully")
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
	return err
}
