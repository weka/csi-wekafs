package apiclient

import (
	"context"
	"fmt"
	qs "github.com/google/go-querystring/query"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"sync"
	"time"
)

type QuotaListRequest struct {
	FilesystemUid uuid.UUID `json:"-" url:"-"`
	GetPath       bool      `url:"get_path"`
}

func (q *QuotaListRequest) getRequiredFields() []string {
	return []string{"FilesystemUid"}
}

func (q *QuotaListRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(q)
}

func (q *QuotaListRequest) getRelatedObject() ApiObject {
	return nil
}

func (q *QuotaListRequest) getApiUrl(a *ApiClient) string {
	return "filesystems/" + q.FilesystemUid.String() + "/quota"
}

func (q *QuotaListRequest) String() string {
	return "QuotaListRequest{" + q.FilesystemUid.String() + "}"
}

type QuotaMap struct {
	sync.RWMutex
	Quotas        map[uint64]*Quota `json:"quotas"`
	FileSystemUid uuid.UUID         `json:"filesystem_uid"`
	LastUpdate    time.Time         `json:"-" url:"-"`
}

func (q *QuotaMap) GetQuotaForInodeId(inodeId uint64) *Quota {
	q.RLock()
	defer q.RUnlock()
	if quota, ok := q.Quotas[inodeId]; !ok {
		return nil // no quota for this inode
	} else {
		return quota
	}
}

//	 {
//	  "quota_id": "0x3558c7ffd0770000:0",
//	  "inode_id": 10092623495168,
//	  "full_path": "default:/testdir/dir8",
//	  "total_bytes": 8192,
//	  "time_over_soft_limit": 100,
//	  "owner": "user",
//	  "data_blocks": 0,
//	  "grace_seconds": null,
//	  "hard_limit_bytes": 1000001536,
//	  "snapName": "",
//	  "snap_view_id": 0,
//	  "path": "/testdir/dir8",
//	  "fsName": "default",
//	  "metadata_blocks": 2,
//	  "soft_limit_bytes": null,
//	  "status": "ACTIVE"
//	}
type QuotaInList struct {
	// the quota in the list is a little different than the full quota object
	InodeId           uint64 `json:"inode_id"`
	TotalBytes        uint64 `json:"total_bytes"`
	TimeOverSoftLimit uint64 `json:"time_over_soft_limit"`
	HardLimitBytes    uint64 `json:"hard_limit_bytes"`
	Path              string `json:"path"`
	SoftLimitBytes    uint64 `json:"soft_limit_bytes"`
	Status            string `json:"status"`
	UsedBytes         uint64 `json:"used_bytes,omitempty"`
	// rest of the fields are not used in this context
}

type QuotaListResponse []*QuotaInList

func (q *QuotaListResponse) SupportsPagination() bool {
	return true
}
func (q *QuotaListResponse) CombinePartialResponse(partial ApiObjectResponse) error {
	// this is a list, so we just append the data
	if partialList, ok := partial.(*QuotaListResponse); ok {
		*q = append(*q, *partialList...)
		return nil
	}
	return fmt.Errorf("invalid partial response")
}

func (a *ApiClient) GetQuotaMap(ctx context.Context, fs *FileSystem) (*QuotaMap, error) {
	op := "GetQuotaListForFileSystem"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx).With().Str("filesystem", fs.Name).Logger()

	if fs == nil || fs.Uid == uuid.Nil {
		return nil, RequestMissingParams
	}

	ret := &QuotaMap{
		FileSystemUid: fs.Uid,
		Quotas:        make(map[uint64]*Quota),
	}

	r := &QuotaListRequest{
		FilesystemUid: fs.Uid,
		GetPath:       false,
	}
	out := &QuotaListResponse{}
	q, err := qs.Values(r)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to encode query parameters for quota list request")
		return nil, RequestMissingParams
	}
	startTime := time.Now()
	err = a.Get(ctx, r.getApiUrl(a), q, out)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to get quota list for filesystem")
		return nil, err
	}
	quotaTs := time.Now()
	for _, q := range *out {
		inodeId := q.InodeId
		ret.Quotas[inodeId] = &Quota{
			FilesystemUid:  fs.Uid,
			InodeId:        q.InodeId,
			UsedBytes:      q.TotalBytes,
			HardLimitBytes: q.HardLimitBytes,
			SoftLimitBytes: q.SoftLimitBytes,
			Status:         q.Status,
			LastUpdateTime: quotaTs,
		}
	}
	ret.LastUpdate = time.Now()
	logger.Trace().Str("filesystem", fs.Name).Dur("duration_ms", time.Since(startTime)).Int("object_count", len(*out)).Msg("Fetched QuotaMap for filesystem")
	return ret, nil
}
