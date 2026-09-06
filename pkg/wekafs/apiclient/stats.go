package apiclient

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	qs "github.com/google/go-querystring/query"
)

// PerfStats holds the performance counters of a single filesystem for one sample interval.
// Timestamp is the time of the sample the values were taken from, and is zero if the backend
// returned no samples at all.
type PerfStats struct {
	ReadBytes      int64 `json:"READ_BYTES,omitempty"`
	WriteBytes     int64 `json:"WRITE_BYTES,omitempty"`
	Writes         int64 `json:"WRITES,omitempty"`
	Reads          int64 `json:"READS,omitempty"`
	ReadLatencyUs  int64 `json:"READ_LATENCY,omitempty"`
	WriteLatencyUs int64 `json:"WRITE_LATENCY,omitempty"`
	Timestamp      time.Time
}

type statGroup map[string]int64

// FsStats is one sample: every requested counter, for every filesystem, at one point in time.
type FsStats struct {
	Stats        statGroup `json:"stats,omitempty"`
	Resolution   int       `json:"resolution,omitempty"`
	Timestamp    time.Time `json:"timestamp,omitempty"`
	FilesystemId int
}

// StatsResponse is the stats endpoint's reply.
//
// It deliberately does not implement ApiObjectResponse: the endpoint is not paginated, and Request
// treats any response implementing that interface as paginated and calls CombinePartialResponse on
// it.
type StatsResponse struct {
	All struct {
		FsStats []FsStats `json:"fs_stats,omitempty"`
	} `json:"all,omitempty"`
}

// parseStatKey splits a stats map key. The backend returns them as "READS[fS: 1]", where the name
// precedes the bracket and the filesystem id follows the "fS: " marker.
func parseStatKey(key string) (name string, fsId int, ok bool) {
	name, rest, found := strings.Cut(key, "[fS: ")
	if !found {
		return "", 0, false
	}
	id, err := strconv.Atoi(strings.TrimSuffix(rest, "]"))
	if err != nil {
		return "", 0, false
	}
	return name, id, true
}

// GetStats extracts one filesystem's counters from the response.
//
// The reply carries a sample per interval, and only the most recent one is reported. The returned
// Timestamp is that sample's: taking values from the newest sample while reporting the timestamp of
// whichever sample happened to come first in the response would make the two disagree.
func (s *StatsResponse) GetStats(fsId int) (*PerfStats, error) {
	var latest *FsStats
	for i := range s.All.FsStats {
		sample := &s.All.FsStats[i]
		if latest == nil || sample.Timestamp.After(latest.Timestamp) {
			latest = sample
		}
	}
	if latest == nil {
		// No samples at all - report zeroes with a zero Timestamp rather than inventing a time.
		return &PerfStats{}, nil
	}

	ret := &PerfStats{Timestamp: latest.Timestamp}
	for key, value := range latest.Stats {
		name, keyFsId, ok := parseStatKey(key)
		if !ok || keyFsId != fsId {
			continue
		}
		switch name {
		case "READS":
			ret.Reads = value
		case "WRITES":
			ret.Writes = value
		case "READ_BYTES":
			ret.ReadBytes = value
		case "WRITE_BYTES":
			ret.WriteBytes = value
		case "READ_LATENCY":
			ret.ReadLatencyUs = value
		case "WRITE_LATENCY":
			ret.WriteLatencyUs = value
		}
	}
	return ret, nil
}

type StatsRequest struct {
	IntervalSeconds   int      `url:"interval,omitempty"`
	Category          string   `url:"category,omitempty"`
	Stats             []string `url:"stat,omitempty"`
	ResolutionSeconds int      `url:"resolution_secs,omitempty"`
	Accumulated       bool     `url:"accumulated,omitempty"`
	ShowInternal      bool     `url:"show_internal,omitempty"`

	Param struct {
		FilesystemId int `url:"fS,omitempty"`
	} `url:"param,omitempty"`
}

func (s StatsRequest) getRequiredFields() []string {
	return []string{"IntervalSeconds", "Category"}
}

func (s StatsRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(s)
}

func (s StatsRequest) getRelatedObject() ApiObject {
	return nil
}

func (s StatsRequest) getApiUrl(a *ApiClient) string {
	return "stats"
}

func (s StatsRequest) String() string {
	return fmt.Sprintf("StatsRequest{fS: %d}", s.Param.FilesystemId)
}

// GetFilesystemPerformanceStats fetches the last minute of performance counters for a filesystem.
func (a *ApiClient) GetFilesystemPerformanceStats(ctx context.Context, fs *FileSystem) (*PerfStats, error) {
	if fs == nil {
		return nil, RequestMissingParams
	}
	fsId := fs.GetFsIdAsInt()
	if fsId < 0 {
		return nil, fmt.Errorf("cannot fetch performance stats, filesystem %s has an unparseable id %q", fs.Name, fs.Id)
	}

	statsReq := &StatsRequest{
		IntervalSeconds:   60,
		Category:          "fs_stats",
		ResolutionSeconds: 60,
		Accumulated:       true,
		ShowInternal:      true,
	}
	statsReq.Param.FilesystemId = fsId

	query, err := qs.Values(statsReq)
	if err != nil {
		return nil, err
	}

	statsResp := &StatsResponse{}
	if err := a.Get(ctx, statsReq.getApiUrl(a), query, statsResp); err != nil {
		return nil, err
	}
	return statsResp.GetStats(fsId)
}
