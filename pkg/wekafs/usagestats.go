package wekafs

import (
	"context"
	"errors"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

type UsageStats struct {
	// Capacity, Used, Free are in bytes, taken from quota
	Capacity int64
	Used     int64
	Free     int64
}

type PvStats struct {
	Usage       *UsageStats
	Performance *apiclient.PerfStats
}

// FetchPvStats fetches the metric for a specific persistent volume.

func (v *Volume) FetchPvStats(ctx context.Context) (*PvStats, error) {
	logger := log.Ctx(ctx)
	usageStats, err := v.fetchPvUsageStats(ctx)
	if err != nil {
		if v.persistentVol != nil {
			logger.Error().Err(err).Str("volume", v.persistentVol.Name).Msg("Failed to fetch usage stats for persistent volume")
		} else {
			logger.Error().Err(err).Str("volume_id", v.GetId()).Msg("Failed to fetch usage stats for volume ID")
		}
		return nil, err
	}

	ret := &PvStats{
		Usage: usageStats,
	}

	performanceStats, err := v.fetchPvPerformanceStats(ctx)
	if err != nil {
		logger.Error().Err(err).Str("volume", v.persistentVol.Name).Msg("Failed to fetch performance stats for persistent volume")
	} else {
		ret.Performance = performanceStats
	}
	return ret, nil
}

func (v *Volume) fetchPvUsageStats(ctx context.Context) (*UsageStats, error) {
	q, err := v.getQuota(ctx)
	if err != nil {
		return nil, err
	}
	if q != nil {
		return &UsageStats{
			Capacity: int64(q.HardLimitBytes),
			Used:     int64(q.UsedBytes),
			Free:     int64(q.HardLimitBytes - q.UsedBytes),
		}, nil
	}
	return nil, errors.New("no usage stats available")
}

func (v *Volume) fetchPvPerformanceStats(ctx context.Context) (*apiclient.PerfStats, error) {
	if v.apiClient == nil {
		return nil, errors.New("no API client available to fetch performance stats")
	}
	if v.isFilesystem() {
		if v.apiClient.SupportsPerFilesystemPerformanceStats() {
			fs, err := v.getFilesystemObj(ctx, true)
			if err != nil {
				return nil, err
			}
			return v.apiClient.GetFilesystemPerformanceStats(ctx, fs)
		}
		return nil, errors.New("per filesystem performance stats not supported by this API client")
	}

	// TODO: Implement per volume performance stats when supported by WEKA.
	// Also, TODO: remove this part when going production as this gives incorrect data for volumes (only for test purposes)
	if v.apiClient.SupportsPerFilesystemPerformanceStats() {
		fs, err := v.getFilesystemObj(ctx, true)
		if err != nil {
			return nil, err
		}
		return v.apiClient.GetFilesystemPerformanceStats(ctx, fs)
	}
	return nil, errors.New("performance stats not supported by this API client")
}
