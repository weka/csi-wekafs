package wekafs

import (
	"context"
	"errors"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"os"
)

func anyMountIncref(ctx context.Context, apiClient *apiclient.ApiClient, m AnyMount) error {
	logger := log.Ctx(ctx)
	if m.getMounter() == nil {
		logger.Error().Msg("Mounter is nil")
		return errors.New("mounter is nil")
	}

	refCount, lock := m.getMounter().getMountMap().LoadOrStore(m.getRefCountIndex())
	lock.Lock()
	defer lock.Unlock()
	if refCount.Load() > 0 && !m.isMounted() {
		logger.Fatal().Msgf("RefCount is %d but mount point %s does not exist", refCount.Load(), m.getMountPoint())
	}
	if refCount.Load() == 0 {
		if err := m.doMount(ctx, apiClient, m.getMountOptions()); err != nil {
			return err
		}
	}
	refCount.Inc()
	logger.Trace().
		Int32("refcount", refCount.Load()).
		Strs("mount_options", m.getMountOptions().Strings()).
		Str("filesystem_name", m.getFsName()).
		Str("mount_point", m.getMountPoint()).
		Msg("RefCount increased")
	return nil
}

func anyMountDecRef(ctx context.Context, m AnyMount) error {
	logger := log.Ctx(ctx)
	if m.getMounter() == nil {
		logger.Error().Msg("Mounter is nil")
		return errors.New("mounter is nil")
	}
	refCount, lock := m.getMounter().getMountMap().LoadOrStore(m.getRefCountIndex())
	lock.Lock()
	defer lock.Unlock()

	if refCount.Load() <= 1 {
		if err := m.doUnmount(ctx); err != nil {
			return err
		}
	}
	refCount.Dec()
	if refCount.Load() < 0 {
		logger.Fatal().Msgf("RefCount became negative on unmounting %s", m.getMountPoint())
	}
	logger.Trace().
		Int32("refcount", refCount.Load()).
		Strs("mount_options", m.getMountOptions().Strings()).
		Str("filesystem_name", m.getFsName()).
		Str("mount_point", m.getMountPoint()).
		Msg("RefCount decreased")
	return nil
}

func anyMountDoUnmount(ctx context.Context, m AnyMount) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.getMountPoint()).Str("filesystem", m.getFsName()).Logger()
	logger.Trace().Strs("mount_options", m.getMountOptions().Strings()).Msg("Performing umount via k8s native mounter")
	err := m.getKMounter().Unmount(m.getMountPoint())
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

func anyMountGetRefCountIndex(m AnyMount) string {
	return m.getMountPoint() + "\x00" + m.getMountOptions().String()
}

func anyMountIsMounted(m AnyMount) bool {
	return PathExists(m.getMountPoint()) && PathIsWekaMount(m.getMountPoint())
}
