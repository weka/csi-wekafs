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

type QMLocks struct {
	sync.RWMutex
	locks sync.Map
}

func NewQMLocks() *QMLocks {
	return &QMLocks{
		locks: sync.Map{},
	}
}

func (qml *QMLocks) GetLock(uid uuid.UUID) *sync.RWMutex {
	qml.RLock()

	if lock, ok := qml.locks.Load(uid); ok {
		qml.RUnlock()
		return lock.(*sync.RWMutex)
	}
	qml.RUnlock()
	newLock := &sync.RWMutex{}
	qml.Lock()
	defer qml.Unlock()
	qml.locks.Store(uid, newLock)
	return newLock
}

func (qml *QMLocks) Delete(uid uuid.UUID) {
	qml.Lock()
	defer qml.Unlock()
	if lock, ok := qml.locks.Load(uid); ok {
		lock.(*sync.RWMutex).Lock() // Lock before deleting to ensure thread safety
		defer lock.(*sync.RWMutex).Unlock()
		qml.Delete(uid)
	}
}

type QuotaMapsPerFilesystem struct {
	sync.RWMutex
	locks     *QMLocks
	QuotaMaps map[uuid.UUID]*apiclient.QuotaMap // map[filesystemUUID]*apiclient.QuotaMap
}

func (qms *QuotaMapsPerFilesystem) GetLock(uid uuid.UUID) *sync.RWMutex {
	return qms.locks.GetLock(uid)
}

func (qms *QuotaMapsPerFilesystem) DeleteLock(uid uuid.UUID) {
	qms.locks.Delete(uid)
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
		locks:     NewQMLocks(),
	}
}
func (ms *MetricsServer) GetQuotaMapForFilesystem(ctx context.Context, fs *apiclient.FileSystem) (*apiclient.QuotaMap, error) {
	component := "GetQuotaMapForFilesystem"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)

	if fs == nil {
		return nil, errors.New("filesystem is nil")
	}
	if fs.Uid == uuid.Nil {
		return nil, errors.New("filesystem UID is empty")
	}

	quotaMap := ms.quotaMaps.GetQuotaMap(fs.Uid)
	if quotaMap != nil {
		return quotaMap, nil
	}
	return nil, errors.New("quota map not found for filesystem")
}

func (ms *MetricsServer) refreshQuotaMapPerFilesystem(ctx context.Context, fs *apiclient.FileSystem, force bool) (*apiclient.QuotaMap, error) {
	component := "refreshQuotaMapPerFilesystem"
	ctx, span := otel.Tracer(TracerName).Start(ctx, component)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("component", component).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	if fs == nil {
		return nil, errors.New("filesystem is nil")
	}

	logger.Debug().Str("filesystem", fs.Name).Msg("Updating QuotaMap for filesystem")
	maplock := ms.quotaMaps.GetLock(fs.Uid)

	startTime := time.Now()

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
	if existingQuotaMap != nil && !force && existingQuotaMap.LastUpdate.Add(ms.getConfig().wekaQuotaMapValidityDuration).After(time.Now()) {
		logger.Debug().Str("filesystem", fs.Name).Msg("QuotaMap is up-to-date, skipping update")
		return existingQuotaMap, nil // no need to update, the quotaMap is already up-to-date
	}

	apiClient := ms.observedFilesystemUids.GetApiClient(fs.Uid)
	if apiClient == nil {
		return nil, fmt.Errorf("no API client found for filesystem UID %s", fs.Uid)
	}

	// update per-fs prometheus metrics: "csi_driver_name", "cluster_guid", "filesystem_name"
	labelValues := []string{ms.getConfig().GetDriver().name, apiClient.ClusterGuid.String(), fs.Name}
	ms.prometheusMetrics.QuotaMapRefreshInvokeCount.WithLabelValues(labelValues...).Inc()
	defer func() {
		dur := time.Since(startTime).Seconds()
		ms.prometheusMetrics.QuotaMapRefreshDurationSeconds.WithLabelValues(labelValues...).Add(dur)
		ms.prometheusMetrics.QuotaMapRefreshDurationHistogram.WithLabelValues(labelValues...).Observe(dur)
	}()

	// eventually this thread is the one that will fetch the updated quotaMap
	maplock.Lock() // lock the quotaMap to ensure thread safety and prevent other threads from reading it while we update
	quotaMap, err := apiClient.GetQuotaMap(ctx, fs)
	if err != nil {
		ms.prometheusMetrics.QuotaMapRefreshFailureCount.WithLabelValues(labelValues...).Inc()
		return nil, fmt.Errorf("failed to fetch QuotaMap for filesystem %s: %w", fs.Name, err)
	}
	ms.prometheusMetrics.QuotaMapRefreshSuccessCount.WithLabelValues(labelValues...).Inc()

	ms.quotaMaps.Lock() // ensure thread safety when updating the quotaMaps
	defer maplock.Unlock()
	defer ms.quotaMaps.Unlock()
	ms.quotaMaps.QuotaMaps[fs.Uid] = quotaMap
	return quotaMap, nil
}
