package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	qs "github.com/google/go-querystring/query"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"k8s.io/helm/pkg/urlutil"
	"time"
)

type Snapshot struct {
	IsWritable    bool      `json:"isWritable" url:"-"`
	FilesystemId  string    `json:"filesystemId" url:"-"`
	FileSystemUid uuid.UUID `json:"fileSystemUid" url:"-"`
	Filesystem    string    `json:"filesystem,omitempty" url:"-"`
	Locator       string    `json:"locator" url:"-"`
	IsRemoving    bool      `json:"isRemoving" url:"-"`
	Name          string    `json:"name" url:"name,omitempty"`
	Progress      int       `json:"progress" url:"-"`
	Uid           uuid.UUID `json:"uid" url:"-"`
	AccessPoint   string    `json:"accessPoint" url:"access_point,omitempty"`
	StowStatus    string    `json:"stowStatus" url:"-"`
	MultiObs      bool      `json:"multiObs" url:"-"`
	Type          string    `json:"type" url:"-"`
	CreationTime  time.Time `json:"creationTime" url:"-"`
	Id            string    `json:"id" url:"-"`
}

func (snap *Snapshot) String() string {
	return fmt.Sprintln("Snapshot(snapUid:", snap.Uid, "name:", snap.Name, "writable:", snap.IsWritable, "locator:", snap.Locator, "created on:", snap.CreationTime)
}

// FindSnapshotsByFilter returns result set of 0-many objects matching filter
func (a *ApiClient) FindSnapshotsByFilter(ctx context.Context, query *Snapshot, resultSet *[]Snapshot) error {
	ret := &[]Snapshot{}
	q, _ := qs.Values(query)
	err := a.Get(ctx, query.GetBasePath(), q, ret)
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

func (a *ApiClient) FindSnapshotsByFilesystem(ctx context.Context, query *FileSystem, resultSet *[]Snapshot) error {
	if query == nil || query.Uid == uuid.Nil {
		return errors.New("cannot search for snapshots without explicit filesystem Uid")
	}
	snapQuery := &Snapshot{
		Filesystem: query.Name,
	}
	return a.FindSnapshotsByFilter(ctx, snapQuery, resultSet)
}

// GetSnapshotByFilter expected to return exactly one result of FindSnapshotsByFilter (error)
func (a *ApiClient) GetSnapshotByFilter(ctx context.Context, query *Snapshot) (*Snapshot, error) {
	op := "GetSnapshotByFilter"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)

	rs := &[]Snapshot{}
	err := a.FindSnapshotsByFilter(ctx, query, rs)
	if err != nil {
		return &Snapshot{}, err
	}
	if *rs == nil || len(*rs) == 0 {
		return &Snapshot{}, ObjectNotFoundError
	}
	if len(*rs) > 1 {
		return &Snapshot{}, MultipleObjectsFoundError
	}
	result := &(*rs)[0]
	return result, nil
}

func (a *ApiClient) GetSnapshotByName(ctx context.Context, name string) (*Snapshot, error) {
	query := &Snapshot{Name: name}
	return a.GetSnapshotByFilter(ctx, query)
}

func (a *ApiClient) GetSnapshotByUid(ctx context.Context, uid uuid.UUID, snap *Snapshot) error {
	ret := &Snapshot{
		Uid: uid,
	}
	return a.Get(ctx, ret.GetApiUrl(), nil, snap)
}

func (a *ApiClient) CreateSnapshot(ctx context.Context, r *SnapshotCreateRequest, snap *Snapshot) error {
	op := "CreateSnapshot"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)
	if !r.hasRequiredFields() {
		return RequestMissingParams
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}

	err = a.Post(ctx, r.getRelatedObject().GetBasePath(), &payload, nil, snap)
	if err != nil {
		return err
	}
	return nil
}

func (a *ApiClient) UpdateSnapshot(ctx context.Context, r *SnapshotUpdateRequest, snap *Snapshot) error {
	op := "UpdateSnapshot"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)
	if !r.hasRequiredFields() {
		return RequestMissingParams
	}
	var payload []byte
	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}
	err = a.Put(ctx, r.getApiUrl(), &payload, nil, snap)
	if err != nil {
		return err
	}
	return nil
}

func (a *ApiClient) DeleteSnapshot(ctx context.Context, r *SnapshotDeleteRequest) error {
	op := "DeleteSnapshot"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)
	if !r.hasRequiredFields() {
		return RequestMissingParams
	}
	apiResponse := &ApiResponse{}
	err := a.Delete(ctx, r.getApiUrl(), nil, nil, apiResponse)
	if err != nil {
		switch t := err.(type) {
		case *ApiNotFoundError:
			return ObjectNotFoundError
		case *ApiBadRequestError:
			for _, c := range t.ApiResponse.ErrorCodes {
				if c == "SnapshotDoesNotExistException" {
					return ObjectNotFoundError
				}
			}
		}
	}
	return err
}

func (snap *Snapshot) GetType() string {
	return "snapshot"
}

func (snap *Snapshot) GetBasePath() string {
	return "snapshots"
}

func (snap *Snapshot) GetApiUrl() string {
	url, err := urlutil.URLJoin(snap.GetBasePath(), snap.Uid.String())
	if err != nil {
		return ""
	}
	return url
}

func (snap *Snapshot) getImmutableFields() []string {
	return []string{
		"Name",
		"FilesystemId",
		"Filesystem",
	}
}

func (snap *Snapshot) EQ(q ApiObject) bool {
	return ObjectsAreEqual(q, snap)
}

func NewSnapshotCreateRequest(name, accessPoint string, fsUid uuid.UUID, sourceSnapUid *uuid.UUID, isWritable bool) (*SnapshotCreateRequest, error) {
	ret := &SnapshotCreateRequest{
		FsUid:         fsUid,
		Name:          name,
		AccessPoint:   accessPoint,
		SourceSnapUid: sourceSnapUid,
		IsWritable:    isWritable,
	}
	return ret, nil
}

type SnapshotCreateRequest struct {
	FsUid         uuid.UUID  `json:"fs_uid"`
	Name          string     `json:"name"`
	AccessPoint   string     `json:"access_point"`
	SourceSnapUid *uuid.UUID `json:"source_snap_uid,omitempty"`
	IsWritable    bool       `json:"is_writable,omitempty"`
}

func (snapc *SnapshotCreateRequest) getApiUrl() string {
	return snapc.getRelatedObject().GetBasePath()
}

func (snapc *SnapshotCreateRequest) getRequiredFields() []string {
	return []string{"Name", "FsUid"}
}

func (snapc *SnapshotCreateRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(snapc)
}

func (snapc *SnapshotCreateRequest) getRelatedObject() ApiObject {
	return &Snapshot{}
}

func (snapc *SnapshotCreateRequest) String() string {
	return fmt.Sprintln("SnapshotCreateRequest(name:", snapc.Name, "access point:", snapc.AccessPoint, "writable:", snapc.IsWritable, snapc.FsUid, ")")
}

type SnapshotUpdateRequest struct {
	Uid         uuid.UUID `json:"-"`
	NewName     string    `json:"new_name"`
	AccessPoint string    `json:"access_point"`
	IsWritable  bool      `json:"is_writable"`
}

func (snapu *SnapshotUpdateRequest) getApiUrl() string {
	url, err := urlutil.URLJoin(snapu.getRelatedObject().GetBasePath(), snapu.Uid.String())
	if err != nil {
		return ""
	}
	return url
}

func (snapu *SnapshotUpdateRequest) getRequiredFields() []string {
	return []string{"Uid"}
}

func (snapu *SnapshotUpdateRequest) getRelatedObject() ApiObject {
	return &Snapshot{}
}

func (snapu *SnapshotUpdateRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(snapu)
}

func (snapu *SnapshotUpdateRequest) String() string {
	return fmt.Sprintln("SnapshotUpdateRequest(fsUid:", snapu.Uid, "writable:", snapu.IsWritable, ")")
}

type SnapshotDeleteRequest struct {
	Uid uuid.UUID `json:"-"`
}

func (snapd *SnapshotDeleteRequest) String() string {
	return fmt.Sprintln("SnapshotDeleteRequest(Uid:", snapd.Uid, ")")
}

func (snapd *SnapshotDeleteRequest) getApiUrl() string {
	url, err := urlutil.URLJoin(snapd.getRelatedObject().GetBasePath(), snapd.Uid.String())
	if err != nil {
		return ""
	}
	return url
}

func (snapd *SnapshotDeleteRequest) getRequiredFields() []string {
	return []string{"Uid"}
}

func (snapd *SnapshotDeleteRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(snapd)
}

func (snapd *SnapshotDeleteRequest) getRelatedObject() ApiObject {
	return &Snapshot{}
}

type SnapshotRestoreRequest struct {
	Uid   uuid.UUID `json:"-"`
	FsUid uuid.UUID `json:"-"`
}

func (snapr *SnapshotRestoreRequest) String() string {
	return fmt.Sprintln("SnapshotRestoreRequest(fsUid:", snapr.Uid, ")")
}

func (snapr *SnapshotRestoreRequest) getApiUrl() string {
	url, err := urlutil.URLJoin(snapr.getRelatedObject().GetBasePath(), snapr.FsUid.String(), snapr.Uid.String(), "restore")
	if err != nil {
		return ""
	}
	return url
}

func (snapr *SnapshotRestoreRequest) getRelatedObject() ApiObject {
	return &Snapshot{}
}

func (snapr *SnapshotRestoreRequest) getRequiredFields() []string {
	return []string{"Uid"}
}

func (snapr *SnapshotRestoreRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(snapr)
}
