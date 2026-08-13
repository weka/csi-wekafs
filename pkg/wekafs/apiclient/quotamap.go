package apiclient

import (
	"context"
	"fmt"
	"sync"
	"time"

	qs "github.com/google/go-querystring/query"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
)

// QuotaListRequest fetches every quota on a filesystem in one call, rather than one request per
// inode. GetPath is left off by default: resolving each quota's path is expensive on the backend
// and callers that only need capacity figures do not use it.
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

// QuotaInList is one entry of the quota listing. The listing returns a wider record than the
// single-quota endpoint; only the fields consumed here are declared.
type QuotaInList struct {
	InodeId        uint64 `json:"inode_id"`
	TotalBytes     uint64 `json:"total_bytes"`
	HardLimitBytes uint64 `json:"hard_limit_bytes"`
	SoftLimitBytes uint64 `json:"soft_limit_bytes"`
	Status         string `json:"status"`
}

// QuotaListResponse is the paginated listing. A filesystem can hold far more quotas than the
// backend returns in one response, so this implements ApiObjectResponse to be fetched page by page.
type QuotaListResponse []*QuotaInList

func (q *QuotaListResponse) CombinePartialResponse(page ApiObjectResponse) error {
	partial, ok := page.(*QuotaListResponse)
	if !ok {
		return fmt.Errorf("cannot combine a %T into a quota listing", page)
	}
	*q = append(*q, *partial...)
	return nil
}

// QuotaMap is every quota on one filesystem, indexed by inode. It is built once by GetQuotaMap and
// then only read; a refresh constructs a new map and replaces the old one wholesale rather than
// mutating it in place.
type QuotaMap struct {
	sync.RWMutex
	Quotas        map[uint64]*Quota `json:"quotas"`
	FileSystemUid uuid.UUID         `json:"filesystem_uid"`
	LastUpdate    time.Time         `json:"-" url:"-"`
}

// GetQuotaForInodeId returns the quota for an inode, or nil when the inode has none.
func (q *QuotaMap) GetQuotaForInodeId(inodeId uint64) *Quota {
	q.RLock()
	defer q.RUnlock()
	return q.Quotas[inodeId]
}

// Len reports how many quotas the map holds.
func (q *QuotaMap) Len() int {
	q.RLock()
	defer q.RUnlock()
	return len(q.Quotas)
}

// GetQuotaMap fetches all quotas of a filesystem in a single paginated listing. Fetching them
// together costs one request per filesystem instead of one per volume, which is the difference
// between a bounded and an unbounded number of API calls as volume count grows.
func (a *ApiClient) GetQuotaMap(ctx context.Context, fs *FileSystem) (*QuotaMap, error) {
	op := "GetQuotaMap"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)

	if fs == nil || fs.Uid == uuid.Nil {
		return nil, RequestMissingParams
	}
	logger := log.Ctx(ctx).With().Str("filesystem", fs.Name).Logger()

	r := &QuotaListRequest{FilesystemUid: fs.Uid}
	query, err := qs.Values(r)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to encode query parameters for quota list request")
		return nil, RequestMissingParams
	}

	startTime := time.Now()
	out := &QuotaListResponse{}
	if err := a.Get(ctx, r.getApiUrl(a), query, out); err != nil {
		logger.Error().Err(err).Msg("Failed to get quota list for filesystem")
		return nil, err
	}

	ret := &QuotaMap{
		FileSystemUid: fs.Uid,
		Quotas:        make(map[uint64]*Quota, len(*out)),
	}
	for _, q := range *out {
		ret.Quotas[q.InodeId] = &Quota{
			FilesystemUid:  fs.Uid,
			InodeId:        q.InodeId,
			// The listing calls consumed capacity total_bytes; Quota calls it UsedBytes,
			// after the name the single-quota endpoint uses. Same quantity.
			UsedBytes:      q.TotalBytes,
			HardLimitBytes: q.HardLimitBytes,
			SoftLimitBytes: q.SoftLimitBytes,
			Status:         q.Status,
		}
	}
	ret.LastUpdate = time.Now()

	logger.Trace().Dur("duration", time.Since(startTime)).Int("object_count", len(*out)).Msg("Fetched QuotaMap for filesystem")
	return ret, nil
}
