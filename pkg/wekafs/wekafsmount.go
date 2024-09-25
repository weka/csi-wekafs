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

type wekafsMount struct {
	fsName                  string
	mountPoint              string
	refCount                int
	lock                    sync.Mutex
	kMounter                mount.Interface
	debugPath               string
	mountOptions            MountOptions
	lastUsed                time.Time
	allowProtocolContainers bool
}

func (m *wekafsMount) getMountPoint() string {
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

func (m *wekafsMount) isInDevMode() bool {
	return m.debugPath != ""
}

func (m *wekafsMount) isMounted() bool {
	return PathExists(m.getMountPoint()) && PathIsWekaMount(context.Background(), m.getMountPoint())
}

func (m *wekafsMount) incRef(ctx context.Context, apiClient *apiclient.ApiClient) error {
	logger := log.Ctx(ctx)
	m.lock.Lock()
	defer m.lock.Unlock()
	if m.refCount < 0 {
		logger.Error().Str("mount_point", m.getMountPoint()).Int("refcount", m.refCount).Msg("During incRef negative refcount encountered")
		m.refCount = 0 // to make sure that we don't have negative refcount later
	}
	if m.refCount == 0 {
		if err := m.doMount(ctx, apiClient, m.getMountOptions()); err != nil {
			return err
		}
	} else if !m.isMounted() {
		logger.Warn().Str("mount_point", m.getMountPoint()).Int("refcount", m.refCount).Msg("Mount not exists although should!")
		if err := m.doMount(ctx, apiClient, m.getMountOptions()); err != nil {
			return err
		}

	}
	m.refCount++
	logger.Trace().Int("refcount", m.refCount).Strs("mount_options", m.getMountOptions().Strings()).Str("filesystem_name", m.fsName).Msg("RefCount increased")
	return nil
}

func (m *wekafsMount) decRef(ctx context.Context) error {
	logger := log.Ctx(ctx)
	m.lock.Lock()
	defer m.lock.Unlock()
	m.refCount--
	m.lastUsed = time.Now()
	logger.Trace().Int("refcount", m.refCount).Strs("mount_options", m.getMountOptions().Strings()).Str("filesystem_name", m.fsName).Msg("RefCount decreased")
	if m.refCount < 0 {
		logger.Error().Int("refcount", m.refCount).Msg("During decRef negative refcount encountered")
		m.refCount = 0 // to make sure that we don't have negative refcount later
	}
	if m.refCount == 0 {
		if err := m.doUnmount(ctx); err != nil {
			m.refCount++ // since we failed to unmount, we need to increase the refcount back to the original value
			return err
		}
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

func (m *wekafsMount) doMount(ctx context.Context, apiClient *apiclient.ApiClient, mountOptions MountOptions) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.getMountPoint()).Str("filesystem", m.fsName).Logger()
	mountToken := ""
	var mountOptionsSensitive []string
	var localContainerName string
	if err := os.MkdirAll(m.getMountPoint(), DefaultVolumePermissions); err != nil {
		return err
	}
	if !m.isInDevMode() {
		pattern := "/proc/wekafs/*/queue"
		containerPaths, err := filepath.Glob(pattern)
		if err != nil {
			logger.Error().Err(err).Msg("Failed to fetch WekaFS containers on host, cannot mount filesystem without Weka container")
			return err
		} else if len(containerPaths) == 0 {
			logger.Error().Err(err).Msg("Failed to find active Weka container, cannot mount filesystem")
			return errors.New("could not perform a mount since active Weka container was not found on host")
		}

		if apiClient == nil {
			// this flow is relevant only for legacy volumes, will not work with SCMC
			logger.Trace().Msg("No API client for mount, not requesting mount token")
		} else {
			if mountToken, err = apiClient.GetMountTokenForFilesystemName(ctx, m.fsName); err != nil {
				return err
			}
			mountOptionsSensitive = append(mountOptionsSensitive, fmt.Sprintf("token=%s", mountToken))
		}

		// if needed, add containerName to the mount string
		if apiClient != nil && len(containerPaths) > 1 {
			localContainerName = apiClient.Credentials.LocalContainerName
			if apiClient.SupportsMultipleClusters() {
				if localContainerName != "" {
					logger.Info().Str("local_container_name", localContainerName).Msg("Local container name set by secrets")
				} else {
					container, err := apiClient.GetLocalContainer(ctx, m.allowProtocolContainers)
					if err != nil || container == nil {
						logger.Warn().Err(err).Msg("Failed to determine local container, assuming default")
					} else {
						localContainerName = container.ContainerName
					}

				}
				if localContainerName != "" {
					for _, p := range containerPaths {
						containerName := filepath.Base(filepath.Dir(p))
						if localContainerName == containerName {
							mountOptions.customOptions["container_name"] = mountOption{
								option: "container_name",
								value:  localContainerName,
							}

							break
						}
					}

				} else {
					logger.Error().Err(errors.New("Could not determine container name, refer to documentation on handling multiple clusters clients with Kubernetes")).Msg("Failed to mount")
				}
			}
		}

		logger.Trace().Strs("mount_options", m.getMountOptions().Strings()).
			Fields(mountOptions).Msg("Performing mount")
		return m.kMounter.MountSensitive(m.fsName, m.getMountPoint(), "wekafs", mountOptions.Strings(), mountOptionsSensitive)
	} else {
		fakePath := filepath.Join(m.debugPath, m.fsName)
		if err := os.MkdirAll(fakePath, DefaultVolumePermissions); err != nil {
			Die(fmt.Sprintf("Failed to create directory %s, while running in debug mode", fakePath))
		}
		logger.Trace().Strs("mount_options", m.getMountOptions().Strings()).Str("debug_path", m.debugPath).Msg("Performing mount")

		return m.kMounter.Mount(fakePath, m.getMountPoint(), "", []string{"bind"})
	}
}
