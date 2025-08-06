package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"k8s.io/mount-utils"
	"os"
	"time"
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

func (m *wekafsMount) isMounted() bool {
	return anyMountIsMounted(m)
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
	logger := log.Ctx(ctx).With().Str("filesystem", m.fsName).Logger()

	// already set
	if m.containerName != "" {
		return nil
	}

	var err error
	if m.containerName, err = apiClient.EnsureLocalContainer(ctx, m.allowProtocolContainers); err != nil {
		logger.Error().Err(err).Msg("Failed to ensure local container")
	}
	m.mountOptions.AddOption(fmt.Sprintf("container_name=%s", m.containerName))
	return nil
}

func (m *wekafsMount) doMount(ctx context.Context, apiClient *apiclient.ApiClient, mountOptions MountOptions) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.getMountPoint()).Str("filesystem", m.fsName).Logger()
	var mountOptionsSensitive []string
	if apiClient == nil {
		logger.Trace().Msg("No API client for mount, cannot proceed")
		return errors.New("no api client bound, cannot obtain mount token")
	}

	if err := os.MkdirAll(m.getMountPoint(), DefaultVolumePermissions); err != nil {
		return err
	}
	if !isWekaRunning() {
		logger.Error().Msg("WEKA is not running, cannot mount. Make sure WEKA client software is running on the host")
		return errors.New("weka is not running, cannot mount")
	}

	logger.Trace().Strs("mount_options", m.getMountOptions().Strings()).Msg("Performing mount")

	if mountToken, err := apiClient.GetMountTokenForFilesystemName(ctx, m.fsName); err != nil {
		return err
	} else {
		mountOptionsSensitive = append(mountOptionsSensitive, fmt.Sprintf("token=%s", mountToken))
	}

	logger.Trace().Strs("mount_options", mountOptions.Strings()).Msg("Performing mount")
	return m.kMounter.MountSensitive(m.fsName, m.getMountPoint(), "wekafs", mountOptions.Strings(), mountOptionsSensitive)
}
