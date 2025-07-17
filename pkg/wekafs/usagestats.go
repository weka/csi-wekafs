package wekafs

import (
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
