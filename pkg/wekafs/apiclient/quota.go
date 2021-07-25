package apiclient

type Quota struct {
	InodeId        int64   `json:"inodeId,omitempty"`
	TotalBytes     int     `json:"totalBytes,omitempty"`
	Owner          string  `json:"owner,omitempty"`
	DataBlocks     int     `json:"dataBlocks,omitempty"`
	GraceSeconds   int64   `json:"graceSeconds,omitempty"`
	HardLimitBytes int     `json:"hardLimitBytes,omitempty"`
	SnapViewId     int     `json:"snapViewId,omitempty"`
	MetadataBlocks int     `json:"metadataBlocks,omitempty"`
	SoftLimitBytes float64 `json:"softLimitBytes,omitempty"`
	Status         string  `json:"status,omitempty"`
}

type QuotaCreateRequest struct {
}
