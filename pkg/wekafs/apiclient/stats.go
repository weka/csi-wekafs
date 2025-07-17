package apiclient

import (
	"context"
	"fmt"
	qs "github.com/google/go-querystring/query"
	"strconv"
	"strings"
	"time"
)

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

type FsStats struct {
	Stats        statGroup `json:"stats,omitempty"`
	Resolution   int       `json:"resolution,omitempty"`
	Timestamp    time.Time `json:"timestamp,omitempty"`
	FilesystemId int
}
type StatsResponse struct {
	All struct {
		FsStats []FsStats `json:"fs_stats,omitempty"`
	} `json:"all,omitempty"`
}

func (s *StatsResponse) SupportsPagination() bool {
	return false
}

func (s *StatsResponse) CombinePartialResponse(next ApiObjectResponse) error {
	panic("implement me")
}

func (s *StatsResponse) GetType() string {
	return "stats"
}

func (s *StatsResponse) GetBasePath(a *ApiClient) string {
	return "stats"
}

func (s *StatsResponse) GetApiUrl(a *ApiClient) string {
	//TODO implement me
	panic("implement me")
}

func (s *StatsResponse) EQ(other ApiObject) bool {
	//TODO implement me
	panic("implement me")
}

func (s *StatsResponse) getImmutableFields() []string {
	//TODO implement me
	panic("implement me")
}

func (s *StatsResponse) String() string {
	//TODO implement me
	panic("implement me")
}

func (s *StatsResponse) GetStats(ctx context.Context, fsId int) (*PerfStats, error) {
	timeZero := time.Time{}
	ret := &PerfStats{}
	for _, fsstat := range s.All.FsStats {
		if ret.Timestamp == timeZero {
			ret.Timestamp = fsstat.Timestamp // take the first timestamp as the initial value
		}
		if fsstat.Timestamp.Before(ret.Timestamp) {
			continue // Skip older stats
		}
		for k, v := range fsstat.Stats {
			// the map keys come in format "READS[fS: 1]" where READS is the name of the stat and "fS: 1" is the filesystem ID
			statName := strings.Split(k, "[")[0]
			fsIdStrs := strings.Split(k, "[fS: ")
			if len(fsIdStrs) < 2 {
				continue // Invalid format, skip this entry
			}
			fsIdStr := fsIdStrs[1]
			fsIdStr = strings.TrimSuffix(fsIdStr, "]")
			fetchedFsId, err := strconv.Atoi(fsIdStr)
			if err != nil {
				continue // If conversion fails, skip this entry
			}
			if fsId == fetchedFsId {
				switch statName {
				case "READS":
					ret.Reads = v
				case "WRITES":
					ret.Writes = v
				case "READ_BYTES":
					ret.ReadBytes = v
				case "WRITE_BYTES":
					ret.WriteBytes = v
				case "READ_LATENCY":
					ret.ReadLatencyUs = v
				case "WRITE_LATENCY":
					ret.WriteLatencyUs = v
				}

			} else {
				// If the filesystem ID does not match, we skip this stat
				continue
			}
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
	return &StatsResponse{}
}

func (s StatsRequest) getApiUrl(a *ApiClient) string {
	return "stats"
}

func (s StatsRequest) String() string {
	return fmt.Sprintf("StatsRequest{fS: %d}", s.Param.FilesystemId)
}

func (a *ApiClient) GetFilesystemPerformanceStats(ctx context.Context, fs *FileSystem) (*PerfStats, error) {
	statsReq := &StatsRequest{
		IntervalSeconds:   60,
		Category:          "fs_stats",
		ResolutionSeconds: 60,
		Accumulated:       true,
		ShowInternal:      true,
	}
	fsId := fs.GetFsIdAsInt()
	statsReq.Param.FilesystemId = fsId

	query, err := qs.Values(statsReq)
	if err != nil {
		return nil, err
	}

	statsResp := &StatsResponse{}
	err = a.Get(ctx, statsReq.getApiUrl(a), query, statsResp)
	if err != nil {
		return nil, err
	}

	return statsResp.GetStats(ctx, fsId)
}
