package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/helm/pkg/urlutil"
	"strconv"
	"strings"
	"time"
)

type QuotaType string
type QuotaStatus string

//goland:noinspection GoUnusedConst
const (
	QuotaTypeHard       QuotaType = "HARD"
	QuotaTypeSoft       QuotaType = "SOFT"
	QuotaTypeDefault              = QuotaTypeHard
	QuotaStatusActive             = "ACTIVE"
	QuotaStatusPending            = "ADDING"
	QuotaStatusError              = "ERROR"
	QuotaStatusDeleting           = "DELETING"
	MaxQuotaSize        uint64    = 9223372036854775807
)

type Quota struct {
	FilesystemUid  uuid.UUID `json:"-"`
	InodeId        uint64    `json:"inode_id,omitempty"`
	UsedBytes      uint64    `json:"used_bytes,omitempty"`
	HardLimitBytes uint64    `json:"hard_limit_bytes,omitempty"`
	SoftLimitBytes uint64    `json:"soft_limit_bytes,omitempty"`
	Status         string    `json:"status,omitempty"`
	LastUpdateTime time.Time `json:"-"`
}

func (q *Quota) SupportsPagination() bool {
	return false
}

func (q *Quota) CombinePartialResponse(next ApiObjectResponse) error {
	panic("implement me")
}

func (q *Quota) String() string {
	return fmt.Sprintln("Quota(fsUid:", q.FilesystemUid, "inodeId:", q.InodeId, "type:", q.GetQuotaType(), "capacity:", q.GetCapacityLimit(), "status:", q.Status, ")")
}

func (q *Quota) GetType() string {
	return "quota"
}

func (q *Quota) GetBasePath(a *ApiClient) string {
	fsUrl := (&FileSystem{Uid: q.FilesystemUid}).GetApiUrl(a)
	url, err := urlutil.URLJoin(fsUrl, q.GetType())
	if err != nil {
		return ""
	}
	return url
}

func (q *Quota) GetApiUrl(a *ApiClient) string {
	url, err := urlutil.URLJoin(q.GetBasePath(a), strconv.FormatUint(q.InodeId, 10))
	if err != nil {
		return ""
	}
	return url
}

func (q *Quota) getImmutableFields() []string {
	return []string{
		"filesystemUid",
		"inodeId",
	}
}

func (q *Quota) EQ(r ApiObject) bool {
	return ObjectsAreEqual(r, q)
}

func (q *Quota) GetQuotaType() QuotaType {
	if q.HardLimitBytes <= q.SoftLimitBytes {
		return QuotaTypeHard
	}
	return QuotaTypeSoft
}

func (q *Quota) GetCapacityLimit() uint64 {
	if q.GetQuotaType() == QuotaTypeHard {
		return q.HardLimitBytes
	}
	return q.SoftLimitBytes
}

type Quotas []*Quota

func (q Quotas) SupportsPagination() bool {
	return true
}
func (q Quotas) CombinePartialResponse(next ApiObjectResponse) error {
	// this is a list, so we just append the data
	if partialList, ok := next.(*Quotas); ok {
		q = append(q, *partialList...)
		return nil
	}
	return fmt.Errorf("invalid partial response")
}

type QuotaCreateRequest struct {
	filesystemUid  uuid.UUID
	inodeId        uint64
	HardLimitBytes uint64 `json:"hard_limit_bytes,omitempty"`
	SoftLimitBytes uint64 `json:"soft_limit_bytes,omitempty"`
	Path           string `json:"path,omitempty"`
	GraceSeconds   uint64 `json:"grace_seconds"`
	quotaType      QuotaType
	capacityLimit  uint64
}

func (qc *QuotaCreateRequest) getApiUrl(a *ApiClient) string {
	return qc.getRelatedObject().GetApiUrl(a)
}

func (qc *QuotaCreateRequest) getRequiredFields() []string {
	return []string{"inodeId", "filesystemUid", "quotaType", "capacityLimit"}
}
func (qc *QuotaCreateRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(qc)
}
func (qc *QuotaCreateRequest) getRelatedObject() ApiObject {
	return &Quota{
		FilesystemUid: qc.filesystemUid,
		InodeId:       qc.inodeId,
	}
}
func (qc *QuotaCreateRequest) String() string {
	return fmt.Sprintln("QuotaCreateRequest(fsUid:", qc.filesystemUid, "inodeId:", qc.inodeId, "type:", qc.quotaType, "capacity:", qc.capacityLimit, ")")
}

type QuotaUpdateRequest struct {
	filesystemUid  uuid.UUID
	inodeId        uint64
	HardLimitBytes uint64 `json:"hard_limit_bytes,omitempty"`
	SoftLimitBytes uint64 `json:"soft_limit_bytes,omitempty"`
	GraceSeconds   uint64 `json:"grace_seconds"`
	quotaType      QuotaType
	capacityLimit  uint64
}

func (qu *QuotaUpdateRequest) getApiUrl(a *ApiClient) string {
	return qu.getRelatedObject().GetApiUrl(a)
}

func (qu *QuotaUpdateRequest) getRequiredFields() []string {
	return []string{"inodeId", "filesystemUid"}
}
func (qu *QuotaUpdateRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(qu)
}
func (qu *QuotaUpdateRequest) getRelatedObject() ApiObject {
	return &Quota{
		FilesystemUid: qu.filesystemUid,
		InodeId:       qu.inodeId,
	}
}
func (qu *QuotaUpdateRequest) String() string {
	return fmt.Sprintln("QuotaUpdateRequest(fsUid:", qu.filesystemUid, "inodeId:", qu.inodeId, "type:", qu.quotaType, "capacity:", qu.capacityLimit, ")")
}

func NewQuotaCreateRequest(fs FileSystem, inodeId uint64, quotaType QuotaType, capacityLimit uint64) *QuotaCreateRequest {
	filesystemUid := fs.Uid
	ret := &QuotaCreateRequest{
		filesystemUid: filesystemUid,
		inodeId:       inodeId,
		quotaType:     quotaType,
		capacityLimit: capacityLimit,
		GraceSeconds:  0,
	}
	if quotaType == QuotaTypeHard {
		ret.HardLimitBytes = capacityLimit
		ret.SoftLimitBytes = capacityLimit
	} else if quotaType == QuotaTypeSoft {
		ret.SoftLimitBytes = capacityLimit
		ret.HardLimitBytes = MaxQuotaSize
	}
	return ret
}

func NewQuotaUpdateRequest(fs FileSystem, inodeId uint64, quotaType QuotaType, capacityLimit uint64) *QuotaUpdateRequest {
	filesystemUid := fs.Uid
	ret := &QuotaUpdateRequest{
		filesystemUid: filesystemUid,
		inodeId:       inodeId,
		quotaType:     quotaType,
		capacityLimit: capacityLimit,
	}
	if quotaType == QuotaTypeHard {
		ret.HardLimitBytes = capacityLimit
		ret.SoftLimitBytes = capacityLimit
	} else if quotaType == QuotaTypeSoft {
		ret.SoftLimitBytes = capacityLimit
		ret.HardLimitBytes = MaxQuotaSize
	}
	return ret
}

func NewQuotaDeleteRequest(fs FileSystem, inodeId uint64) *QuotaDeleteRequest {
	return &QuotaDeleteRequest{
		filesystemUid: fs.Uid,
		InodeId:       inodeId,
	}
}

type QuotaDeleteRequest struct {
	filesystemUid uuid.UUID
	InodeId       uint64 `json:"inodeId,omitempty"`
	Path          string `json:"path"`
}

func (qd *QuotaDeleteRequest) String() string {
	return fmt.Sprintln("QuotaDeleteRequest(fsUid:", qd.filesystemUid, "inodeId:", qd.InodeId, ")")
}

func (qd *QuotaDeleteRequest) getApiUrl(a *ApiClient) string {
	url, err := urlutil.URLJoin((&FileSystem{Uid: qd.filesystemUid}).GetApiUrl(a), "quotas", strconv.FormatUint(qd.InodeId, 10))
	if err != nil {
		return ""
	}
	return url
}

func (qd *QuotaDeleteRequest) getRequiredFields() []string {
	return []string{"filesystemUid", "inodeId"}
}

func (qd *QuotaDeleteRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(qd)
}

func (qd *QuotaDeleteRequest) getRelatedObject() ApiObject {
	return &Quota{
		FilesystemUid: qd.filesystemUid,
		InodeId:       qd.InodeId,
	}
}

func (a *ApiClient) CreateQuota(ctx context.Context, qr *QuotaCreateRequest, q *Quota, waitForCompletion bool) error {
	op := "CreateQuota"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	if !qr.hasRequiredFields() {
		return RequestMissingParams
	}
	payload, err := json.Marshal(qr)
	if err != nil {
		return err
	}

	err = a.Put(ctx, qr.getApiUrl(a), &payload, nil, q)
	if err != nil {
		return err
	}
	if waitForCompletion {
		q.FilesystemUid = qr.filesystemUid
		if q.InodeId != qr.inodeId { // WEKAPP-240948
			q.InodeId = qr.inodeId
		}
		return a.WaitForQuotaActive(ctx, q)
	}
	return nil
}

func (a *ApiClient) WaitForQuotaActive(ctx context.Context, q *Quota) error {
	log.Ctx(ctx).Debug().Uint64("inode_id", q.InodeId).Str("filesystem_uid", q.FilesystemUid.String()).
		Msg("Waiting for quota to become active")
	f := wait.ConditionFunc(func() (bool, error) {
		return a.IsQuotaActive(ctx, q)
	})
	err := wait.Poll(1*time.Second, time.Hour*24, f)
	if err != nil {
		log.Ctx(ctx).Error().Err(err).Msg("")
		return err
	}
	return nil
}

func (a *ApiClient) FindQuotaByFilter(ctx context.Context, query *Quota, resultSet *Quotas) error {
	if query.FilesystemUid == uuid.Nil {
		return RequestMissingParams
	}
	ret := &Quotas{}
	err := a.Get(ctx, query.GetBasePath(a), nil, ret)
	if err != nil {
		return err
	}
	for _, r := range *ret {
		r.FilesystemUid = query.FilesystemUid
		if r.EQ(query) {
			*resultSet = append(*resultSet, r)
		}
	}
	return nil
}

func (a *ApiClient) GetQuotaByFileSystemAndInode(ctx context.Context, fs *FileSystem, inodeId uint64) (*Quota, error) {
	op := "GetQuotaByFileSystemAndInode"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx).With().Str("filesystem", fs.Name).Uint64("inode_id", inodeId).Logger()
	if fs == nil || inodeId == 0 {
		return nil, RequestMissingParams
	}
	ret := &Quota{
		FilesystemUid: fs.Uid,
		InodeId:       inodeId,
	}
	err := a.Get(ctx, ret.GetApiUrl(a), nil, ret)
	if err != nil {
		switch t := err.(type) {
		case *ApiNotFoundError:
			return nil, ObjectNotFoundError
		case *ApiInternalError:
			if strings.Contains(t.ApiResponse.Message, "Directory has no quota") {
				return nil, ObjectNotFoundError
			}
			return nil, err
		case *ApiNonTransientError:
			if strings.Contains(t.Error(), "Directory has no quota") {
				return nil, ObjectNotFoundError
			}

		default:
			logger.Error().Err(t).Msg("Invalid condition on getting quota")
			return nil, err
		}
	}
	ret.FilesystemUid = fs.Uid
	ret.InodeId = inodeId
	return ret, nil
}

func (a *ApiClient) GetQuotaByFilter(ctx context.Context, query *Quota) (*Quota, error) {
	rs := &Quotas{}
	err := a.FindQuotaByFilter(ctx, query, rs)
	if err != nil {
		return nil, err
	}
	if *rs == nil || len(*rs) == 0 {
		return nil, ObjectNotFoundError
	}
	if len(*rs) > 1 {
		return nil, MultipleObjectsFoundError
	}
	result := &(*rs)[0]
	return *result, nil
}

func (a *ApiClient) IsQuotaActive(ctx context.Context, query *Quota) (done bool, err error) {
	fs := &FileSystem{
		Uid: query.FilesystemUid,
	}
	q, err := a.GetQuotaByFileSystemAndInode(ctx, fs, query.InodeId)
	if err != nil {
		return false, err
	}
	if q != nil {
		// TODO: add quotaStatusError, quotaStatusDeleting, quotaStatusPending handling
		return q.Status == QuotaStatusActive, nil
	}
	return false, nil
}

func (a *ApiClient) UpdateQuota(ctx context.Context, r *QuotaUpdateRequest, q *Quota) error {
	op := "UpdateQuota"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	//if !r.hasRequiredFields() {
	//	return RequestMissingParams
	//}
	var payload []byte
	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}
	err = a.Put(ctx, r.getApiUrl(a), &payload, nil, q)
	if err != nil {
		return err
	}
	return nil
}

func (a *ApiClient) DeleteQuota(ctx context.Context, r *QuotaDeleteRequest) error {
	op := "DeleteQuota"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	if !r.hasRequiredFields() {
		return RequestMissingParams
	}
	apiResponse := &ApiResponse{}
	err := a.Delete(ctx, r.getApiUrl(a), nil, nil, apiResponse)
	if err != nil {
		return err
	}
	return nil
}
