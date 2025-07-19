package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"go.opentelemetry.io/otel"
	"sync"
	"time"
)

type QuotaMapsPerFilesystem struct {
	sync.RWMutex
	locks     sync.Map
	QuotaMaps map[uuid.UUID]*apiclient.QuotaMap // map[filesystemUUID]*apiclient.QuotaMap
}

func (qms *QuotaMapsPerFilesystem) GetQuotaMap(uid uuid.UUID) *apiclient.QuotaMap {
	qms.RLock()
	defer qms.RUnlock()
	quotaMap, exists := qms.QuotaMaps[uid]
	if !exists {
		return nil
	}
	return quotaMap
}

func NewQuotaMapsPerFilesystem() *QuotaMapsPerFilesystem {
	return &QuotaMapsPerFilesystem{
		QuotaMaps: make(map[uuid.UUID]*apiclient.QuotaMap),
		locks:     sync.Map{},
	}
}
func (ms *MetricsServer) GetQuotaMapForFilesystem(ctx context.Context, fs *apiclient.FileSystem) (*apiclient.QuotaMap, error) {
	component := "GetQuotaMapForFilesystem"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	if fs == nil {
		return nil, errors.New("filesystem is nil")
	}
	if fs.Uid == uuid.Nil {
		return nil, errors.New("filesystem UID is empty")
	}

	ms.quotaMaps.RLock()
	quotaMap, exists := ms.quotaMaps.QuotaMaps[fs.Uid]
	ms.quotaMaps.RUnlock()

	var maplock *sync.RWMutex

	l, ok := ms.quotaMaps.locks.Load(fs.Uid)
	if !ok || l == nil {
		l = &sync.RWMutex{}
		ms.quotaMaps.locks.Store(fs.Uid, l)
	}
	maplock = l.(*sync.RWMutex)

	if exists {
		// lock the quotaMap to ensure thread safety
		maplock.RLock()
		if quotaMap.LastUpdate.Add(ms.getConfig().wekaMetricsFetchInterval).After(time.Now()) {
			logger.Trace().Str("filesystem", fs.Name).Msg("Returning cached QuotaMap for filesystem")
			maplock.RUnlock()
			return quotaMap, nil
		}
		maplock.RUnlock()
	}

	// Re-check if the quotaMap was updated while we were waiting for the lock
	ms.quotaMaps.RLock()
	quotaMap, exists = ms.quotaMaps.QuotaMaps[fs.Uid]
	ms.quotaMaps.RUnlock()
	if exists { // maybe it was updated while we were waiting for the lock
		// lock the quotaMap to ensure thread safety
		if quotaMap.LastUpdate.Add(ms.getConfig().wekaMetricsFetchInterval).After(time.Now()) {
			logger.Trace().Str("filesystem", fs.Name).Msg("Returning cached QuotaMap for filesystem")
			return quotaMap, nil
		}
	}
	// If we reach here, we need to wait for the update to complete
	return nil, errors.New("quota map not found for filesystem")
}

func (ms *MetricsServer) refreshQuotaMapPerFilesystem(ctx context.Context, fs *apiclient.FileSystem, force bool) error {
	component := "refreshQuotaMapPerFilesystem"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	var maplock *sync.RWMutex

	l, ok := ms.quotaMaps.locks.Load(fs.Uid)
	if !ok || l == nil {
		l = &sync.RWMutex{}
		ms.quotaMaps.locks.Store(fs.Uid, l)
	}
	maplock = l.(*sync.RWMutex)

	startTime := time.Now()

	logger.Debug().Str("filesystem", fs.Name).Msg("Updating QuotaMap for filesystem")
	defer func() {
		dur := time.Since(startTime)
		logger.Debug().Str("filesystem", fs.Name).Dur("duration", dur).Msg("Finished Updating QuotaMap for filesystem")
		if dur > ms.getConfig().wekaMetricsFetchInterval {
			logger.Warn().Str("filesystem", fs.Name).Dur("duration", dur).Msg("Updating QuotaMap took longer than expected. Consider increasing wekaMetricsFetchInterval")
		} else {
			logger.Trace().Str("filesystem", fs.Name).Dur("duration", dur).Msg("Updating QuotaMap completed within expected time")
		}
	}()

	// optimization: if quota map still valid, skip update unless force flag is set
	existingQuotaMap := ms.quotaMaps.GetQuotaMap(fs.Uid)
	if existingQuotaMap != nil && !force && existingQuotaMap.LastUpdate.Add(ms.getConfig().wekaMetricsFetchInterval).After(time.Now()) {
		logger.Trace().Str("filesystem", fs.Name).Msg("QuotaMap is up-to-date, skipping update")
		return nil // no need to update, the quotaMap is already up-to-date
	}

	apiClient := ms.observedFilesystemUids.GetApiClient(fs.Uid)
	if apiClient == nil {
		return fmt.Errorf("no API client found for filesystem UID %s", fs.Uid)
	}

	// update per-fs prometheus metrics: "csi_driver_name", "cluster_guid", "filesystem_name"
	labelValues := []string{ms.getConfig().GetDriver().name, apiClient.ClusterGuid.String(), fs.Name}
	ms.prometheusMetrics.QuotaMapUpdateCountPerFsInvokeCount.WithLabelValues(labelValues...).Inc()
	defer func() {
		dur := time.Since(startTime).Seconds()
		ms.prometheusMetrics.QuotaMapUpdateDurationPerFs.WithLabelValues(labelValues...).Add(dur)
		ms.prometheusMetrics.QuotaMapUpdateHistogramPerFs.WithLabelValues(labelValues...).Observe(dur)
	}()

	// eventually this thread is the one that will fetch the updated quotaMap
	quotaMap, err := apiClient.GetQuotaMap(ctx, fs)
	if err != nil {
		ms.prometheusMetrics.QuotaMapUpdateCountPerFsFailureCount.WithLabelValues(labelValues...).Inc()
		return fmt.Errorf("failed to fetch QuotaMap for filesystem %s: %w", fs.Name, err)
	}
	ms.prometheusMetrics.QuotaMapUpdateCountPerFsSuccessCount.WithLabelValues(labelValues...).Inc()

	ms.quotaMaps.Lock() // ensure thread safety when updating the quotaMaps
	maplock.Lock()
	defer maplock.Unlock()
	defer ms.quotaMaps.Unlock()
	ms.quotaMaps.QuotaMaps[fs.Uid] = quotaMap
	return nil
}
