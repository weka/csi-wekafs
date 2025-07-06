package apiclient

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestStatsResponse_GetStats(t *testing.T) {
	// Example JSON to be supplied by you
	jsonData := `{
		"all": {
		  "fs_stats": [
			{
			  "stats": {
				"READS[fS: 1]": 1,
				"READ_LATENCY[fS: 1]": 2,
				"WRITES[fS: 1]": 3,
				"WRITE_LATENCY[fS: 1]": 4,
				"WRITE_BYTES[fS: 1]": 5,
				"READ_BYTES[fS: 1]": 6
			  },
			  "resolution": 60,
			  "timestamp": "2025-07-10T16:58:00Z"
			},
			{
			  "stats": {
				"READS[fS: 1]": 7,
				"READ_LATENCY[fS: 1]": 8,
				"WRITES[fS: 1]": 9,
				"WRITE_LATENCY[fS: 1]": 10,
				"WRITE_BYTES[fS: 1]": 11,
				"READ_BYTES[fS: 1]": 12
			  },
			  "resolution": 60,
			  "timestamp": "2025-07-10T16:59:00Z"
			}
		  ]
		}
	  }`

	statsResp := &StatsResponse{}
	err := json.Unmarshal([]byte(jsonData), statsResp)
	assert.NoError(t, err)

	fsId := 1 // Set this to the filesystem ID you want to test
	perfStats, err := statsResp.GetStats(context.Background(), fsId)
	assert.NoError(t, err)
	assert.NotNil(t, perfStats)

	// Add assertions for expected values, e.g.:
	// assert.Equal(t, int64(123), perfStats.Reads)
	// assert.Equal(t, int64(456), perfStats.Writes)
}
