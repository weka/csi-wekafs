package apiclient

import (
	"encoding/json"
	"github.com/golang/glog"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/helm/pkg/urlutil"
	"math"
	"strconv"
	"time"
)

type QuotaType string
type QuotaStatus string

const QuotaTypeHard QuotaType = "HARD"
const QuotaTypeSoft QuotaType = "SOFT"
const QuotaStatusActive = "ACTIVE"
const QuotaStatusPending = "PENDING"
const QuotaStatusError = "ERROR"

type Quota struct {
	InodeId        uint64    `json:"inodeId,omitempty"`
	TotalBytes     uint64    `json:"totalBytes,omitempty"`
	Owner          string    `json:"owner,omitempty"`
	DataBlocks     uint64    `json:"dataBlocks,omitempty"`
	GraceSeconds   uint64    `json:"graceSeconds,omitempty"`
	HardLimitBytes uint64    `json:"hardLimitBytes,omitempty"`
	SnapViewId     uint64    `json:"snapViewId,omitempty"`
	MetadataBlocks uint64    `json:"metadataBlocks,omitempty"`
	SoftLimitBytes uint64    `json:"softLimitBytes,omitempty"`
	Status         string    `json:"status,omitempty"`
	FilesystemUid  uuid.UUID `json:"-"`
	QuotaType      QuotaType `json:"-"`
	CapacityLimit  uint64    `json:"-"`
}

func (q *Quota) GetType() string {
	return "quota"
}

func (q *Quota) GetBasePath() string {
	url, err := urlutil.URLJoin((&FileSystem{Uid: q.FilesystemUid}).GetApiUrl(), "quotas")
	if err != nil {
		return ""
	}
	return url
}

func (q *Quota) GetApiUrl() string {
	url, err := urlutil.URLJoin(q.GetBasePath(), strconv.FormatUint(q.InodeId, 10))
	if err != nil {
		return url
	}
	return ""
}

func (q *Quota) getImmutableFields() []string {
	return []string{
		"FilesystemUid",
		"InodeId",
	}
}

func (q *Quota) EQ(r ApiObject) bool {
	return ObjectsAreEqual(r, q)
}

func (q *Quota) GetQuotaType() QuotaType {
	if q.HardLimitBytes < q.SoftLimitBytes {
		return QuotaTypeHard
	}
	return QuotaTypeSoft
}

func (q *Quota) GetCapacityLimit() uint64 {
	if q.QuotaType == QuotaTypeHard {
		return q.HardLimitBytes
	}
	return q.SoftLimitBytes
}

type QuotaCreateRequest struct {
	FilesystemUid  uuid.UUID `json:"-,omitempty"`
	InodeId        uint64    `json:"inodeId,omitempty"`
	QuotaType      QuotaType `json:"-"`
	CapacityLimit  uint64    `json:"-"`
	HardLimitBytes uint64    `json:"hardLimitBytes,omitempty"`
	SoftLimitBytes uint64    `json:"softLimitBytes,omitempty"`
}

func (qc *QuotaCreateRequest) getApiUrl() string {
	return qc.getRelatedObject().GetBasePath()
}

func (qc *QuotaCreateRequest) getRequiredFields() []string {
	return []string{"InodeId", "FilesystemUid", "QuotaType", "CapacityLimit"}
}
func (qc *QuotaCreateRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(qc)
}
func (qc *QuotaCreateRequest) getRelatedObject() ApiObject {
	return &Quota{}
}

type QuotaUpdateRequest struct {
	FilesystemUid  uuid.UUID `json:"-,omitempty"`
	InodeId        uint64    `json:"inodeId,omitempty"`
	QuotaType      QuotaType `json:"-"`
	CapacityLimit  uint64    `json:"-"`
	HardLimitBytes uint64    `json:"hardLimitBytes,omitempty"`
	SoftLimitBytes uint64    `json:"softLimitBytes,omitempty"`
}

func (qu *QuotaUpdateRequest) getApiUrl() string {
	return qu.getRelatedObject().GetBasePath()
}

func (qu *QuotaUpdateRequest) getRequiredFields() []string {
	return []string{"InodeId", "FilesystemUid", "QuotaType", "CapacityLimit"}
}
func (qu *QuotaUpdateRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(qu)
}
func (qu *QuotaUpdateRequest) getRelatedObject() ApiObject {
	return &Quota{}
}

func NewQuotaCreateRequest(fs FileSystem, inodeId uint64, quotaType QuotaType, capacityLimit uint64) *QuotaCreateRequest {
	filesystemUid := fs.Uid
	ret := &QuotaCreateRequest{
		FilesystemUid: filesystemUid,
		InodeId:       inodeId,
	}
	if quotaType == QuotaTypeHard {
		ret.HardLimitBytes = capacityLimit
		ret.SoftLimitBytes = math.MaxInt64
	} else if quotaType == QuotaTypeSoft {
		ret.SoftLimitBytes = capacityLimit
		ret.HardLimitBytes = math.MaxInt64
	}
	return ret
}

func NewQuotaUpdateRequest(fs FileSystem, inodeId uint64, quotaType QuotaType, capacityLimit uint64) *QuotaUpdateRequest {
	filesystemUid := fs.Uid
	ret := &QuotaUpdateRequest{
		FilesystemUid: filesystemUid,
		InodeId:       inodeId,
	}
	if quotaType == QuotaTypeHard {
		ret.HardLimitBytes = capacityLimit
		ret.SoftLimitBytes = math.MaxInt64
	} else if quotaType == QuotaTypeSoft {
		ret.SoftLimitBytes = capacityLimit
		ret.HardLimitBytes = math.MaxInt64
	}
	return ret
}

type QuotaDeleteRequest struct {
	FilesystemUid uuid.UUID `json:"-"`
	InodeId       uint64    `json:"inodeId,omitempty"`
}

func (qd *QuotaDeleteRequest) getApiUrl() string {
	url, err := urlutil.URLJoin((&FileSystem{Uid: qd.FilesystemUid}).GetApiUrl(), "quotas", strconv.FormatUint(qd.InodeId, 10))
	if err != nil {
		return ""
	}
	return url
}

func (qd *QuotaDeleteRequest) getRequiredFields() []string {
	return []string{"FilesystemUid", "InodeId"}
}

func (qd *QuotaDeleteRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(qd)
}

func (qd *QuotaDeleteRequest) getRelatedObject() ApiObject {
	return &Quota{}
}

func (a *ApiClient) CreateQuota(qr *QuotaCreateRequest, q *Quota, waitForCompletion bool) error {
	if !qr.hasRequiredFields() {
		return RequestMissingParams
	}
	payload, err := json.Marshal(qr)
	if err != nil {
		return err
	}

	err = a.Post(qr.getApiUrl(), &payload, nil, q)
	if err != nil {
		return err
	}
	if waitForCompletion {
		return a.WaitForQuotaActive(q)
	}
	return nil
}

func (a *ApiClient) WaitForQuotaActive(q *Quota) error {
	glog.V(4).Infof("Waiting for quota %s@%s to become active", q.InodeId, q.FilesystemUid.String())
	f := wait.ConditionFunc(func() (bool, error) {
		return a.IsQuotaActive(q)
	})
	err := wait.Poll(5*time.Second, time.Hour*24, f)
	if err != nil {
		return err
	}
	return nil
}

func (a *ApiClient) FindQuotaByFilter(query *Quota, resultSet *[]Quota) error {
	ret := &[]Quota{}
	err := a.Get(query.GetBasePath(), nil, ret)
	if err != nil {
		return err
	}
	for _, r := range *ret {
		if r.EQ(query) {
			*resultSet = append(*resultSet, r)
		}
	}
	return nil
}

func (a *ApiClient) GetQuotaByFilter(query *Quota) (*Quota, error) {
	rs := &[]Quota{}
	err := a.FindQuotaByFilter(query, rs)
	if err != nil {
		return nil, err
	}
	if *rs == nil || len(*rs) == 0 {
		return nil, ApiObjectNotFoundError
	}
	if len(*rs) > 1 {
		return nil, ApiMultipleObjectsFoundError
	}
	result := &(*rs)[0]
	return result, nil
}

func (a *ApiClient) IsQuotaActive(query *Quota) (done bool, err error) {
	q, err := a.GetQuotaByFilter(query)
	if err != nil {
		return false, err
	}
	if q != nil {
		return q.Status == QuotaStatusActive, nil
	}
	return false, nil
}

func (a *ApiClient) UpdateQuota(r *QuotaUpdateRequest, q *Quota) error {
	if !r.hasRequiredFields() {
		return RequestMissingParams
	}
	var payload []byte
	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}
	err = a.Put(r.getApiUrl(), &payload, nil, q)
	if err != nil {
		return err
	}
	return nil
}

func (a *ApiClient) DeleteQuota(r *QuotaDeleteRequest) error {
	if !r.hasRequiredFields() {
		return RequestMissingParams
	}
	apiResponse := &ApiResponse{}
	err := a.Delete(r.getApiUrl(), nil, nil, apiResponse)
	if err != nil {
		return err
	}
	return nil
}
