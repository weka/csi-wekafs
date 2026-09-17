package apiclient

import (
	"encoding/json"
	"testing"
	"time"

	qs "github.com/google/go-querystring/query"
)

// Two samples a minute apart, for two filesystems, listed oldest-first as the backend returns them
// chronologically. The ordering matters: an implementation that latches the timestamp from the
// first entry it sees while continuing to take values from later ones reports the newest sample's
// counters stamped with the oldest sample's time.
const statsFixture = `{
  "all": {
    "fs_stats": [
      {
        "stats": {
          "READS[fS: 1]": 1, "READ_LATENCY[fS: 1]": 2, "WRITES[fS: 1]": 3,
          "WRITE_LATENCY[fS: 1]": 4, "WRITE_BYTES[fS: 1]": 5, "READ_BYTES[fS: 1]": 6
        },
        "resolution": 60,
        "timestamp": "2025-07-10T16:58:00Z"
      },
      {
        "stats": {
          "READS[fS: 1]": 7, "READ_LATENCY[fS: 1]": 8, "WRITES[fS: 1]": 9,
          "WRITE_LATENCY[fS: 1]": 10, "WRITE_BYTES[fS: 1]": 11, "READ_BYTES[fS: 1]": 12,
          "READS[fS: 2]": 700, "READ_BYTES[fS: 2]": 1200
        },
        "resolution": 60,
        "timestamp": "2025-07-10T16:59:00Z"
      }
    ]
  }
}`

// The same two samples in the opposite order, since the newest-first case takes a different path
// through any "skip older samples" logic.
const statsFixtureNewestFirst = `{
  "all": {
    "fs_stats": [
      {
        "stats": {"READS[fS: 1]": 7, "READ_BYTES[fS: 1]": 12},
        "resolution": 60,
        "timestamp": "2025-07-10T16:59:00Z"
      },
      {
        "stats": {"READS[fS: 1]": 1, "READ_BYTES[fS: 1]": 6},
        "resolution": 60,
        "timestamp": "2025-07-10T16:58:00Z"
      }
    ]
  }
}`

// Whichever order the samples arrive in, the newest one wins and its timestamp is reported.
func TestStatsResponseGetStatsSampleOrdering(t *testing.T) {
	newest := time.Date(2025, 7, 10, 16, 59, 0, 0, time.UTC)
	for name, fixture := range map[string]string{
		"oldest first": statsFixture,
		"newest first": statsFixtureNewestFirst,
	} {
		t.Run(name, func(t *testing.T) {
			resp := &StatsResponse{}
			if err := json.Unmarshal([]byte(fixture), resp); err != nil {
				t.Fatalf("failed to unmarshal fixture: %v", err)
			}
			got, err := resp.GetStats(1)
			if err != nil {
				t.Fatalf("GetStats returned an error: %v", err)
			}
			if got.Reads != 7 || got.ReadBytes != 12 {
				t.Errorf("values came from the wrong sample: %+v", *got)
			}
			if !got.Timestamp.Equal(newest) {
				t.Errorf("Timestamp = %v, want %v - the values are the newest sample's, so the "+
					"timestamp must be too", got.Timestamp, newest)
			}
		})
	}
}

func TestStatsResponseGetStats(t *testing.T) {
	resp := &StatsResponse{}
	if err := json.Unmarshal([]byte(statsFixture), resp); err != nil {
		t.Fatalf("failed to unmarshal fixture: %v", err)
	}

	got, err := resp.GetStats(1)
	if err != nil {
		t.Fatalf("GetStats returned an error: %v", err)
	}

	// Values must come from the newest sample (16:59), not the first one in the document.
	want := &PerfStats{
		Reads: 7, ReadLatencyUs: 8, Writes: 9, WriteLatencyUs: 10,
		WriteBytes: 11, ReadBytes: 12,
		Timestamp: time.Date(2025, 7, 10, 16, 59, 0, 0, time.UTC),
	}
	if *got != *want {
		t.Errorf("GetStats(1) =\n  %+v\nwant\n  %+v", *got, *want)
	}
	// The timestamp must identify the sample the values came from, not merely be non-zero.
	if !got.Timestamp.Equal(want.Timestamp) {
		t.Errorf("Timestamp = %v, want the newest sample's %v", got.Timestamp, want.Timestamp)
	}
}

// Counters of other filesystems in the same sample must not leak in.
func TestStatsResponseGetStatsFiltersByFilesystem(t *testing.T) {
	resp := &StatsResponse{}
	if err := json.Unmarshal([]byte(statsFixture), resp); err != nil {
		t.Fatalf("failed to unmarshal fixture: %v", err)
	}

	got, err := resp.GetStats(2)
	if err != nil {
		t.Fatalf("GetStats returned an error: %v", err)
	}
	if got.Reads != 700 || got.ReadBytes != 1200 {
		t.Errorf("GetStats(2) = %+v, want Reads=700 ReadBytes=1200", *got)
	}
	if got.Writes != 0 || got.WriteBytes != 0 {
		t.Errorf("GetStats(2) picked up filesystem 1's writes: %+v", *got)
	}

	// A filesystem with no counters in the sample yields zeroes, not another filesystem's numbers.
	other, err := resp.GetStats(99)
	if err != nil {
		t.Fatalf("GetStats returned an error: %v", err)
	}
	if other.Reads != 0 || other.ReadBytes != 0 || other.Writes != 0 {
		t.Errorf("GetStats(99) = %+v, want all zeroes", *other)
	}
}

func TestStatsResponseGetStatsEmpty(t *testing.T) {
	resp := &StatsResponse{}
	got, err := resp.GetStats(1)
	if err != nil {
		t.Fatalf("GetStats returned an error: %v", err)
	}
	if !got.Timestamp.IsZero() {
		t.Errorf("Timestamp = %v, want zero when there are no samples", got.Timestamp)
	}
	if got.Reads != 0 {
		t.Errorf("Reads = %d, want 0", got.Reads)
	}
}

func TestParseStatKey(t *testing.T) {
	for _, tc := range []struct {
		key     string
		name    string
		fsId    int
		ok      bool
		comment string
	}{
		{"READS[fS: 1]", "READS", 1, true, "the normal shape"},
		{"WRITE_LATENCY[fS: 42]", "WRITE_LATENCY", 42, true, "multi-digit id"},
		{"READS", "", 0, false, "no filesystem marker"},
		{"READS[fS: abc]", "", 0, false, "unparseable id"},
		{"READS[fS: ]", "", 0, false, "empty id"},
		{"", "", 0, false, "empty key"},
	} {
		name, fsId, ok := parseStatKey(tc.key)
		if ok != tc.ok || name != tc.name || fsId != tc.fsId {
			t.Errorf("parseStatKey(%q) = (%q, %d, %v), want (%q, %d, %v) - %s",
				tc.key, name, fsId, ok, tc.name, tc.fsId, tc.ok, tc.comment)
		}
	}
}

func TestFileSystemGetFsIdAsInt(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want int
	}{
		{"FSId<0>", 0},
		{"FSId<7>", 7},
		{"FSId<123>", 123},
		{"", -1},
		{"FSId<>", -1},
		{"garbage", -1},
	} {
		if got := (&FileSystem{Id: tc.id}).GetFsIdAsInt(); got != tc.want {
			t.Errorf("FileSystem{Id: %q}.GetFsIdAsInt() = %d, want %d", tc.id, got, tc.want)
		}
	}
}

// Filesystem ids start at zero: the API reports the first one as "FSId<0>", GetFsIdAsInt parses it
// to 0, and only -1 means unknown. With omitempty on the query field that id encoded to nothing, so
// the stats request carried no filesystem filter at all and returned cluster-wide counters as if
// they belonged to that one filesystem - wrong numbers, silently, rather than an error.
func TestStatsRequestKeepsFilesystemIdZero(t *testing.T) {
	req := &StatsRequest{IntervalSeconds: 60, Category: "fs_stats"}
	req.Param.FilesystemId = 0

	values, err := qs.Values(req)
	if err != nil {
		t.Fatalf("expected the request to encode, got %v", err)
	}

	if got := values.Get("param[fS]"); got != "0" {
		t.Errorf("expected filesystem id 0 to be sent, got %q (query %q)", got, values.Encode())
	}

	// And a non-zero id must still be sent, so the fix cannot have broken the ordinary case.
	req.Param.FilesystemId = 7
	values, err = qs.Values(req)
	if err != nil {
		t.Fatalf("expected the request to encode, got %v", err)
	}
	if got := values.Get("param[fS]"); got != "7" {
		t.Errorf("expected filesystem id 7 to be sent, got %q (query %q)", got, values.Encode())
	}
}

// The request must name the counters it wants. Left empty, the endpoint returns every fs_stats
// counter, and statGroup is a map[string]int64 - so one non-integer value among them fails the
// unmarshal for the whole reply and the fetch returns nothing at all.
//
// Asserted against what GetStats actually reads, so the two cannot drift: a counter requested but
// not handled is dead weight, and one handled but not requested always reads zero.
func TestFilesystemPerformanceCountersMatchWhatGetStatsReads(t *testing.T) {
	if len(filesystemPerformanceCounters) == 0 {
		t.Fatal("expected the request to name its counters, got none - the reply would carry everything")
	}

	// Feed one sample containing every requested counter, and require each to land somewhere.
	stats := statGroup{}
	for i, name := range filesystemPerformanceCounters {
		stats[name+"[fS: 3]"] = int64(i + 1)
	}
	resp := &StatsResponse{}
	resp.All.FsStats = []FsStats{{Stats: stats, Timestamp: time.Unix(100, 0)}}

	got, err := resp.GetStats(3)
	if err != nil {
		t.Fatalf("expected the sample to decode, got %v", err)
	}

	zero := 0
	for _, v := range []int64{got.Reads, got.Writes, got.ReadBytes, got.WriteBytes, got.ReadLatencyUs, got.WriteLatencyUs} {
		if v == 0 {
			zero++
		}
	}
	if zero != 0 {
		t.Errorf("expected every requested counter to be read into PerfStats, %d were ignored", zero)
	}
}

// And the request carries them, so the fix is not just a declaration.
func TestGetFilesystemPerformanceStatsRequestNamesItsCounters(t *testing.T) {
	req := &StatsRequest{IntervalSeconds: 60, Category: "fs_stats", Stats: filesystemPerformanceCounters}
	values, err := qs.Values(req)
	if err != nil {
		t.Fatalf("expected the request to encode, got %v", err)
	}
	if got := values["stat"]; len(got) != len(filesystemPerformanceCounters) {
		t.Errorf("expected %d counters on the wire, got %v", len(filesystemPerformanceCounters), got)
	}
}
