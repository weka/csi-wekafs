/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package wekafs

import (
	"context"
	"sync/atomic"

	"github.com/rs/zerolog/log"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// Reference counting is identical for both transports, so it lives here rather than in two copies
// that drift. What genuinely differs stays on each type: how a mount is made and torn down, and how
// its refcount index is spelled, since the NFS mount normalises its options first.
//
// The entry's lock is held across doMount and doUnmount on purpose. Two callers arriving together
// would otherwise both read a zero refcount and both mount. It is a per-entry lock, so this
// serialises callers of one mount rather than every mount on the node.

func anyMountIncRef(ctx context.Context, m AnyMount, mm *mountMap, apiClient *apiclient.ApiClient) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.getMountPoint()).Logger()

	return mm.WithEntry(m.getRefcountIdx(), func(refCount *atomic.Int32) error {
		switch {
		case refCount.Load() == 0:
			logger.Debug().Strs("mount_options", m.getMountOptions().Strings()).
				Msg("No existing mount, mounting filesystem")
			if err := m.doMount(ctx, apiClient, m.getMountOptions()); err != nil {
				return err
			}
		case !m.isMounted(ctx):
			// The refcount says someone holds this mount but it is not in /proc/mounts - the host lost
			// it underneath us. Remount rather than hand the caller a path that is not mounted.
			logger.Warn().Int32("refcount", refCount.Load()).
				Msg("Mount not found in /proc/mounts despite positive refcount, remounting")
			if err := m.doMount(ctx, apiClient, m.getMountOptions()); err != nil {
				return err
			}
		}

		logger.Debug().Int32("refcount", refCount.Add(1)).
			Strs("mount_options", m.getMountOptions().Strings()).Msg("Mount refcount incremented")
		return nil
	})
}

func anyMountDecRef(ctx context.Context, m AnyMount, mm *mountMap) error {
	logger := log.Ctx(ctx).With().Str("mount_point", m.getMountPoint()).Logger()

	return mm.WithEntry(m.getRefcountIdx(), func(refCount *atomic.Int32) error {
		current := refCount.Load()
		if current <= 0 {
			// Nothing to release. Logged rather than fatal: an unmount arriving twice must not take
			// the node server down with it.
			logger.Error().Int32("refcount", current).
				Msg("Refcount not positive during decRef, nothing to release")
			return nil
		}

		if current == 1 {
			// The last holder unmounts - but only if the mount is still there. Unmounting a path the
			// host already lost would fail, and returning that error would leave the refcount stuck at
			// one forever: the sweep only prunes at zero, so every later release would fail the same
			// way and the entry would never be reclaimed.
			if m.isMounted(ctx) {
				if err := m.doUnmount(ctx); err != nil {
					return err
				}
			} else {
				logger.Warn().Msg("Mount already gone from /proc/mounts, releasing last reference without unmounting")
			}
		}

		logger.Debug().Int32("refcount", refCount.Add(-1)).
			Strs("mount_options", m.getMountOptions().Strings()).Msg("Mount refcount decremented")
		return nil
	})
}

// anyMountIsMounted answers for both transports: a Weka mount looks the same in /proc/mounts whether
// it got there over NFS or natively.
func anyMountIsMounted(ctx context.Context, m AnyMount) bool {
	return PathExists(m.getMountPoint()) && PathIsWekaMount(ctx, m.getMountPoint())
}
