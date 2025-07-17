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
	sync.Mutex
	locks     sync.Map
	QuotaMaps map[uuid.UUID]*apiclient.QuotaMap // map[filesystemUUID]*apiclient.QuotaMap
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

	ms.quotaMaps.Lock()
	quotaMap, exists := ms.quotaMaps.QuotaMaps[fs.Uid]
	ms.quotaMaps.Unlock()

	var maplock *sync.Mutex
	l, ok := ms.quotaMaps.locks.Load(fs.Uid)
	if !ok || l == nil {
		l = &sync.Mutex{}
		ms.quotaMaps.locks.Store(fs.Uid, l)
	}
	maplock = l.(*sync.Mutex)

	if exists {
		// lock the quotaMap to ensure thread safety
		maplock.Lock()
		if quotaMap.LastUpdate.Add(ms.getConfig().wekaMetricsFetchInterval).After(time.Now()) {
			logger.Trace().Str("filesystem", fs.Name).Msg("Returning cached QuotaMap for filesystem")
			maplock.Unlock()
			return quotaMap, nil
		}
	}
	// If quotaMap does not exist or is stale, fetch it from the API
	logger.Debug().Msg("Fetching QuotaMap for filesystem from API")

	apiClient := ms.observedFilesystemUids.GetApiClient(fs.Uid)
	if apiClient == nil {
		return nil, fmt.Errorf("no API client found for filesystem UID %s", fs.Uid)
	}
	maplock.Lock()
	defer maplock.Unlock()

	if exists { // maybe it was updated while we were waiting for the lock
		// lock the quotaMap to ensure thread safety
		maplock.Lock()
		if quotaMap.LastUpdate.Add(ms.getConfig().wekaMetricsFetchInterval).After(time.Now()) {
			logger.Trace().Str("filesystem", fs.Name).Msg("Returning cached QuotaMap for filesystem")
			maplock.Unlock()
			return quotaMap, nil
		}
	}

	quotaMap, err := apiClient.GetQuotaMap(ctx, fs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch QuotaMap for filesystem %s: %w", fs.Name, err)
	}

	ms.quotaMaps.Lock() // ensure thread safety when updating the quotaMaps
	defer ms.quotaMaps.Unlock()
	ms.quotaMaps.QuotaMaps[fs.Uid] = quotaMap
	return quotaMap, nil
}
