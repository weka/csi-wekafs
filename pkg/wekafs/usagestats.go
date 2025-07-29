package wekafs

import (
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"time"
)

type UsageStats struct {
	// Capacity, Used, Free are in bytes, taken from quota
	Capacity  int64
	Used      int64
	Free      int64
	Timestamp time.Time
}

type PvStats struct {
	Usage       *UsageStats
	Performance *apiclient.PerfStats
}
