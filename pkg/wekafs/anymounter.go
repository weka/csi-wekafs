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
	"strings"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

// Mount bookkeeping that does not depend on the transport. Both mounters keep the same shape of map
// and sweep it the same way, so the sweeps live here and each mounter passes in its own map.

// mounterState is the per-mounter bookkeeping that carries no transport of its own, embedded by both
// mounters rather than written out twice.
type mounterState struct {
	// Flipped by Probe as the Weka client appears or disappears under a running node server, and read
	// from the gRPC handlers, so it is atomic rather than a plain bool.
	enabled atomic.Bool
	// SELinux status is resolved once and cached: the check shells out, and the answer cannot change
	// under a running kernel.
	selinuxSupport *bool
}

func (s *mounterState) Enable()         { s.enabled.Store(true) }
func (s *mounterState) Disable()        { s.enabled.Store(false) }
func (s *mounterState) isEnabled() bool { return s.enabled.Load() }

// forceSelinux records SELinux as supported without probing for it, for the -selinux-support flag.
// Seeded at construction, so getSelinuxStatus below never runs its detection.
func (s *mounterState) forceSelinux() {
	forced := true
	s.selinuxSupport = &forced
}

func (s *mounterState) getSelinuxStatus(ctx context.Context) bool {
	if s.selinuxSupport != nil && *s.selinuxSupport {
		return true
	}
	selinuxSupport := getSelinuxStatus(ctx)
	s.selinuxSupport = &selinuxSupport
	return *s.selinuxSupport
}

// refIndexParts splits a refcount index back into its mount point and options for logging. The
// separator is the one getRefcountIdx uses, and a malformed index still logs rather than panicking.
func refIndexParts(refIndex string) (mountPoint, options string) {
	mountPoint, options, _ = strings.Cut(refIndex, "^")
	return mountPoint, options
}

func anyMounterLogActiveMounts(ctx context.Context, mm *mountMap, transport DataTransport) {
	if mm.Len() == 0 {
		return
	}

	active := 0
	for _, refIndex := range mm.Indexes() {
		refCount, _, ok := mm.Load(refIndex)
		if !ok {
			// Pruned by the sweep running alongside this one; nothing to report.
			continue
		}
		mountPoint, options := refIndexParts(refIndex)
		count := refCount.Load()
		logger := log.Ctx(ctx).With().
			Str("mount_point", mountPoint).Str("mount_options", options).
			Str("transport", string(transport)).Int32("refcount", count).Logger()
		if count > 0 {
			logger.Trace().Msg("Mount is active")
			active++
		} else {
			logger.Trace().Msg("Mount is not active")
		}
	}
	log.Ctx(ctx).Debug().Str("transport", string(transport)).
		Int("total", mm.Len()).Int("active", active).Msg("Periodic checkup on mount map")
}

func anyMounterGcInactiveMounts(ctx context.Context, mm *mountMap) {
	for _, refIndex := range mm.Indexes() {
		refCount, lock, ok := mm.Load(refIndex)
		if !ok {
			continue
		}
		// The refcount is re-read under the lock: a caller may have taken this mount between the
		// snapshot above and here, and pruning it then would forget a mount that is still held.
		lock.Lock()
		if refCount.Load() == 0 {
			mountPoint, options := refIndexParts(refIndex)
			log.Ctx(ctx).Trace().Str("mount_point", mountPoint).Str("mount_options", options).
				Msg("Removing inactive mount from map")
			mm.Prune(refIndex)
		}
		lock.Unlock()
	}
}

func anyMounterSchedulePeriodicMountGc(ctx context.Context, m AnyMounter) {
	go func() {
		log.Ctx(ctx).Debug().Str("transport", string(m.getTransport())).Msg("Initializing periodic mount GC")
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			m.LogActiveMounts(ctx)
			m.gcInactiveMounts(ctx)
			// Selecting on the context rather than sleeping, so the sweep stops with the driver instead
			// of outliving it by up to the tick interval.
			select {
			case <-ctx.Done():
				log.Ctx(ctx).Debug().Str("transport", string(m.getTransport())).Msg("Stopping periodic mount GC")
				return
			case <-ticker.C:
			}
		}
	}()
}
