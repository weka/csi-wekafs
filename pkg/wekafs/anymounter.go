package wekafs

import (
	"context"
	"github.com/rs/zerolog/log"
	"strings"
	"time"
)

func anyMounterLogActiveMounts(m AnyMounter) {
	if m.getMountMap().Len() > 0 {
		count := 0
		for _, refIndex := range m.getMountMap().getIndexes() {
			// no need to lock here, as this is a periodic check
			// and we are not modifying the map, just reading it
			refCount, _ := m.getMountMap().Load(refIndex)
			parts := strings.Split(refIndex, "\x00")
			c := int32(0)
			if refCount != nil {
				c = refCount.Load()
				logger := log.With().Str("mount_point", parts[0]).Str("mount_options", parts[1]).Str("ref_index", refIndex).Int32("refcount", c).Logger()
				if c > 0 {
					logger.Trace().Msg("Mount is active")
					count++
				} else {
					logger.Trace().Msg("Mount is not active")
				}

			}
		}
		log.Debug().Int("total", m.getMountMap().Len()).Int("active", count).Msg("Periodic checkup on mount map")
	}
}

func anyMounterGcInactiveMounts(m AnyMounter) {
	if m.getMountMap().Len() > 0 {
		for _, refIndex := range m.getMountMap().getIndexes() {
			refCount, lock := m.getMountMap().Load(refIndex)
			lock.Lock()
			c := refCount.Load()
			if c == 0 {
				parts := strings.Split(refIndex, "\x00")
				logger := log.With().Str("mount_point", parts[0]).Str("mount_options", parts[1]).Str("ref_index", refIndex).Logger()
				logger.Trace().Msg("Removing inactive mount from map")
				m.getMountMap().Prune(refIndex)
			}
			lock.Unlock()
		}
	}
}

func anyMounterGetSelinuxStatus(ctx context.Context, m AnyMounter) bool {
	s := m.getSelinuxSupport()
	if s != nil && *s {
		return true
	}
	selinuxSupport := getSelinuxStatus(ctx)
	m.setSelinuxSupport(selinuxSupport)
	return selinuxSupport
}

func anyMounterSchedulePeriodicMountGc(m AnyMounter) {
	go func() {
		log.Debug().Msgf("Initializing periodic mount GC for %s transport", m.getTransport())
		for true {
			m.LogActiveMounts()
			m.gcInactiveMounts()
			time.Sleep(10 * time.Minute)
		}
	}()
}
