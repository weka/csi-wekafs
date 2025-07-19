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
	"strconv"
	"strings"
	"time"
)

type FileSystem struct {
	Id                   string    `json:"id" url:"id,omitempty"`
	Name                 string    `json:"name" url:"name,omitempty"`
	Uid                  uuid.UUID `json:"uid" url:"-"`
	IsRemoving           bool      `json:"is_removing,omitempty" url:"-"`
	GroupId              string    `json:"group_id" url:"-"`
	IsCreating           bool      `json:"is_creating" url:"-"`
	FreeTotal            int64     `json:"free_total" url:"-"`
	IsEncrypted          bool      `json:"is_encrypted" url:"-"`
	MetadataBudget       int64     `json:"metadata_budget" url:"-"`
	UsedTotalData        int64     `json:"used_total_data" url:"-"`
	UsedTotal            int64     `json:"used_total" url:"-"`
	SsdBudget            int64     `json:"ssd_budget" url:"-"`
	IsReady              bool      `json:"is_ready" url:"-"`
	GroupName            string    `json:"group_name" url:"group_name,omitempty"`
	AvailableTotal       int64     `json:"available_total" url:"-"`
	Status               string    `json:"status" url:"status,omitempty"`
	UsedSsdMetadata      int64     `json:"used_ssd_metadata" url:"-"`
	AuthRequired         bool      `json:"auth_required" url:"-"`
	AvailableSsdMetadata int64     `json:"available_ssd_metadata" url:"-"`
	TotalCapacity        int64     `json:"total_budget" url:"-"`
	UsedSsd              int64     `json:"used_ssd_data" url:"-"`
	AvailableSsd         int64     `json:"available_ssd" url:"-"`
	FreeSsd              int64     `json:"free_ssd" url:"-"`

	ObsBuckets     []interface{} `json:"obs_buckets" url:"-"`
	ObjectStorages []interface{} `json:"object_storages" url:"-"`

	KmsKeyIdentifier string `json:"kms_key_identifier,omitempty" url:"-"`
	KmsNamespace     string `json:"kms_namespace,omitempty" url:"-"`
	KmsRole          string `json:"kms_role,omitempty" url:"-"`

	// used for internal purposes
	ForceFresh *bool `json:"-" url:"force_fresh,omitempty"`
}

type fsCacheEntry struct {
	fs        *FileSystem
	timestamp time.Time
	cacheHits int
}

func (fs *FileSystem) SupportsPagination() bool {
	return false
}

func (fs *FileSystem) CombinePartialResponse(next ApiObjectResponse) error {
	panic("implement me")
}

func (fs *FileSystem) GetFsIdAsInt() int {
	// takes the Id in string format "FSId<0>" and extracts the number from it
	if fs.Id == "" {
		return -1
	}
	idStr := strings.TrimPrefix(fs.Id, "FSId<")
	idStr = strings.TrimSuffix(idStr, ">")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		log.Error().Err(err).Str("id", fs.Id).Msg("Failed to convert filesystem Id to int")
		return -1
	}
	return id
}

type FileSystems []FileSystem

func (f FileSystems) SupportsPagination() bool {
	return true
}

func (f FileSystems) CombinePartialResponse(next ApiObjectResponse) error {
	if nextFs, ok := next.(*FileSystems); ok {
		f = append(f, *nextFs...)
		return nil
	}
	return fmt.Errorf("invalid partial response type: %T", next)
}

type FileSystemMountToken struct {
	Token          string `json:"mount_token,omitempty"`
	FilesystemName string `json:"filesystem_name,omitempty"`
}

func (f FileSystemMountToken) SupportsPagination() bool {
	return false
}

func (f FileSystemMountToken) CombinePartialResponse(next ApiObjectResponse) error {
	panic("implement me")
}

var AsyncOperationTimedOut = errors.New("Asynchronous operation timed out")
var ObjectMarkedForDeletion = errors.New("Object is marked for deletion!")

func (fs *FileSystem) String() string {
	return fmt.Sprintln("FileSystem(fsUid:", fs.Uid, "name:", fs.Name, "capacity:", strconv.FormatInt(fs.TotalCapacity, 10), ")")
}

func (a *ApiClient) GetFileSystemByUid(ctx context.Context, uid uuid.UUID, fs *FileSystem, forceFresh bool) error {
	ret := &FileSystem{
		Uid:        uid,
		ForceFresh: &forceFresh,
	}

	q, _ := qs.Values(ret)
	err := a.Get(ctx, ret.GetApiUrl(a), q, fs)
	if err != nil {
		switch t := err.(type) {
		case *ApiNotFoundError:
			return ObjectNotFoundError
		case *ApiBadRequestError:
			for _, c := range t.ApiResponse.ErrorCodes {
				if c == "FilesystemDoesNotExistException" {
					return ObjectNotFoundError
				}
			}
		default:
			return err
		}
	}
	return nil
}

// FindFileSystemsByFilter returns result set of 0-many objects matching filter
func (a *ApiClient) FindFileSystemsByFilter(ctx context.Context, query *FileSystem, resultSet *[]FileSystem) error {
	op := "FindFileSystemsByFilter"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	ret := &FileSystems{}
	q, _ := qs.Values(query)
	err := a.Get(ctx, query.GetBasePath(a), q, ret)
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

// GetFileSystemByFilter expected to return exactly one result of FindFileSystemsByFilter (error)
func (a *ApiClient) GetFileSystemByFilter(ctx context.Context, query *FileSystem) (*FileSystem, error) {
	rs := &[]FileSystem{}
	err := a.FindFileSystemsByFilter(ctx, query, rs)
	if err != nil {
		return &FileSystem{}, err
	}
	if *rs == nil || len(*rs) == 0 {
		return &FileSystem{}, ObjectNotFoundError
	}
	if len(*rs) > 1 {
		return &FileSystem{}, MultipleObjectsFoundError
	}
	result := &(*rs)[0]
	return result, nil
}

func (a *ApiClient) GetFileSystemByName(ctx context.Context, name string) (*FileSystem, error) {
	query := &FileSystem{Name: name}
	return a.GetFileSystemByFilter(ctx, query)
}

// CachedGetFileSystemByName returns a cached filesystem object by name.
// If forceFresh is true, it fetches a new object and updates the cache.
func (a *ApiClient) CachedGetFileSystemByName(ctx context.Context, name string, forceFresh bool) (*FileSystem, error) {
	const cacheTTL = 30 * time.Second

	a.fsCacheMu.Lock()
	if a.fsCache == nil {
		a.fsCache = make(map[string]*fsCacheEntry)
	}
	entry, found := a.fsCache[name]
	a.fsCacheMu.Unlock()

	if found && !forceFresh && time.Since(entry.timestamp) < cacheTTL {
		entry.cacheHits++
		return entry.fs, nil
	}

	fs, err := a.GetFileSystemByName(ctx, name)
	if err != nil {
		return nil, err
	}

	a.fsCacheMu.Lock()
	a.fsCache[name] = &fsCacheEntry{fs: fs, timestamp: time.Now()}
	a.fsCacheMu.Unlock()

	return fs, nil
}

func (a *ApiClient) CreateFileSystem(ctx context.Context, r *FileSystemCreateRequest, fs *FileSystem) error {
	op := "CreateFileSystem"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	if !r.hasRequiredFields() {
		return RequestMissingParams
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}

	err = a.Post(ctx, r.getRelatedObject().GetBasePath(a), &payload, nil, fs)
	if err != nil {
		return err
	}
	fsName := r.Name
	waitPeriodMax := time.Second * 30

	fs, err = a.WaitFilesystemReady(ctx, fsName, waitPeriodMax)
	if err != nil {
		return errors.New(fmt.Sprintln("Failed to create a file system after", waitPeriodMax.String(), err.Error()))
	}
	return nil
}

func (a *ApiClient) WaitFilesystemReady(ctx context.Context, fsName string, waitPeriodMax time.Duration) (*FileSystem, error) {
	logger := log.Ctx(ctx).With().Str("filesysem", fsName).Logger()
	for start := time.Now(); time.Since(start) < waitPeriodMax; {
		fs, err := a.GetFileSystemByName(ctx, fsName)
		if err != nil || fs == nil {
			logger.Debug().Msg("Filesystem not exists")
			continue
		}
		if fs.IsReady {
			logger.Debug().TimeDiff("created_after", time.Now(), start).Msg("Filesystem is ready")
			return fs, nil
		}
		if fs.IsCreating {
			logger.Debug().Msg("Filesystem still creating")
		}
		if fs.IsRemoving {
			return fs, ObjectMarkedForDeletion
		}
		time.Sleep(time.Second)
	}
	return nil, AsyncOperationTimedOut
}

func (a *ApiClient) UpdateFileSystem(ctx context.Context, r *FileSystemResizeRequest, fs *FileSystem) error {
	op := "UpdateFileSystem"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	if !r.hasRequiredFields() {
		return RequestMissingParams
	}
	var payload []byte
	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}
	err = a.Put(ctx, r.getApiUrl(a), &payload, nil, fs)
	if err != nil {
		return err
	}
	return nil
}

func (a *ApiClient) DeleteFileSystem(ctx context.Context, r *FileSystemDeleteRequest) error {
	op := "DeleteFileSystem"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	if !r.hasRequiredFields() {
		return RequestMissingParams
	}
	apiResponse := &ApiResponse{}
	err := a.Delete(ctx, r.getApiUrl(a), nil, nil, apiResponse)
	if err != nil {
		switch t := err.(type) {
		case *ApiNotFoundError:
			return ObjectNotFoundError
		case *ApiBadRequestError:
			for _, c := range t.ApiResponse.ErrorCodes {
				if c == "FilesystemDoesNotExistException" {
					return ObjectNotFoundError
				}
			}
		}
	}
	return nil
}

func (a *ApiClient) EnsureNoNfsPermissionsForFilesystem(ctx context.Context, fsName string) error {
	logger := log.Ctx(ctx)
	logger.Trace().Str("filesystem", fsName).Msg("Ensuring no NFS permissions for filesystem")
	permissions := &NfsPermissions{}
	err := a.FindNfsPermissionsByFilesystem(ctx, fsName, permissions)
	if err != nil {
		logger.Error().Err(err).Str("filesystem", fsName).Msg("Failed to list NFS permissions")
	}
	if len(*permissions) > 0 {
		logger.Debug().Int("permissions", len(*permissions)).Str("filesystem", fsName).Msg("Found stale NFS permissions, deleting")
	}
	for _, p := range *permissions {
		for i := 0; i < 5; i++ {
			err = a.DeleteNfsPermission(ctx, &NfsPermissionDeleteRequest{Uid: p.Uid})
			if err == nil {
				break
			}
			time.Sleep(time.Second)
		}
		if err != nil {
			logger.Error().Err(err).Str("permission", p.Uid.String()).Str("filesystem", p.Filesystem).Str("client_group", p.Group).Msg("Failed to delete NFS permission after 5 attempts")
			return err
		}
	}
	if len(*permissions) > 0 {
		time.Sleep(time.Second * 5) // wait for NFS permissions reconfiguration
		logger.Trace().Str("filesystem", fsName).Msg("Deleted NFS permissions")
	}
	return nil
}

func (a *ApiClient) GetFileSystemMountToken(ctx context.Context, r *FileSystemMountTokenRequest, token *FileSystemMountToken) error {
	op := "GetFileSystemMountToken"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	if !r.hasRequiredFields() {
		return RequestMissingParams
	}
	err := a.Get(ctx, r.getApiUrl(a), nil, token)
	if err != nil {
		log.Ctx(ctx).Error().Err(err).Msg("Failed to obtain a mount token")
		return err
	}
	return nil
}

func (a *ApiClient) GetMountTokenForFilesystemName(ctx context.Context, fsName string) (string, error) {
	logger := log.Ctx(ctx)
	if !a.SupportsAuthenticatedMounts() {
		logger.Debug().Msg("Current version of Weka cluster does not support authenticated mounts")
		return "", nil
	}
	filesystem, err := a.GetFileSystemByName(ctx, fsName)
	if err != nil {
		return "", err
	}
	req := &FileSystemMountTokenRequest{Uid: filesystem.Uid}
	token := &FileSystemMountToken{}
	err = a.GetFileSystemMountToken(ctx, req, token)
	if err != nil {
		return "", err
	}
	if token.FilesystemName != fsName {
		logger.Error().Msg("Inconsistent mount token obtained, does not match the filesystem")
		return "", errors.New(fmt.Sprintf(
			"failed to fetch mount token, got token for different filesystem name, %s, %s",
			fsName, token.FilesystemName),
		)
	}
	return token.Token, nil
}

func (fs *FileSystem) GetType() string {
	return "filesystem"
}

//goland:noinspection GoUnusedParameter
func (fs *FileSystem) GetBasePath(a *ApiClient) string {
	return "fileSystems"
}

func (fs *FileSystem) GetApiUrl(a *ApiClient) string {
	url, err := urlutil.URLJoin(fs.GetBasePath(a), fs.Uid.String())
	if err != nil {
		return ""
	}
	return url
}

func (fs *FileSystem) getImmutableFields() []string {
	return []string{
		"Name",
		"TotalCapacity",
		"GroupName",
		"Id",
		//"Uid",
	}
}

func (fs *FileSystem) EQ(q ApiObject) bool {
	return ObjectsAreEqual(q, fs)
}

type FileSystemCreateRequest struct {
	Name          string `json:"name"`
	GroupName     string `json:"group_name"`
	TotalCapacity int64  `json:"total_capacity"`
	ObsName       string `json:"obs_name,omitempty"`
	SsdCapacity   *int64 `json:"ssd_capacity,omitempty"`
	Encrypted     bool   `json:"encrypted,omitempty"`
	AuthRequired  bool   `json:"auth_required,omitempty"`
	AllowNoKms    bool   `json:"allow_no_kms,omitempty"`

	KmsVaultKeyIdentifier string `json:"kms_vault_key_identifier,omitempty"`
	KmsVaultNamespace     string `json:"kms_vault_namespace,omitempty"`
	KmsVaultRoleId        string `json:"kms_vault_role_id,omitempty"`
	KmsVaultSecretId      string `json:"kms_vault_secret_id,omitempty"`
}

func (fsc *FileSystemCreateRequest) getApiUrl(a *ApiClient) string {
	return fsc.getRelatedObject().GetBasePath(a)
}

func (fsc *FileSystemCreateRequest) getRequiredFields() []string {
	return []string{"Name", "GroupName", "TotalCapacity"}
}
func (fsc *FileSystemCreateRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(fsc)
}
func (fsc *FileSystemCreateRequest) getRelatedObject() ApiObject {
	return &FileSystem{}
}

func (fsc *FileSystemCreateRequest) String() string {
	return fmt.Sprintln("FileSystemCreateRequest(name:", fsc.Name, "groupName:", fsc.GroupName, "capacity:", fsc.TotalCapacity, ")")
}

func NewFilesystemCreateRequest(name, groupName string, totalCapacity int64, encrytionParams EncryptionParams) (*FileSystemCreateRequest, error) {

	ret := &FileSystemCreateRequest{
		Name:                  name,
		GroupName:             groupName,
		TotalCapacity:         totalCapacity,
		Encrypted:             encrytionParams.Encrypted,
		AllowNoKms:            encrytionParams.AllowNoKms,
		KmsVaultKeyIdentifier: encrytionParams.KmsVaultKeyIdentifier,
		KmsVaultNamespace:     encrytionParams.KmsVaultNamespace,
		KmsVaultRoleId:        encrytionParams.KmsVaultRoleId,
		KmsVaultSecretId:      encrytionParams.KmsVaultSecretId,
	}
	return ret, nil
}

type FileSystemResizeRequest struct {
	Uid           uuid.UUID `json:"-"`
	TotalCapacity *int64    `json:"total_capacity,omitempty"`
}

func NewFileSystemResizeRequest(fsUid uuid.UUID, totalCapacity *int64) *FileSystemResizeRequest {
	ret := &FileSystemResizeRequest{
		Uid: fsUid,
	}
	if totalCapacity != nil {
		ret.TotalCapacity = totalCapacity
	}
	return ret
}

func (fsu *FileSystemResizeRequest) getApiUrl(a *ApiClient) string {
	url, err := urlutil.URLJoin(fsu.getRelatedObject().GetBasePath(a), fsu.Uid.String())
	if err != nil {
		return ""
	}
	return url
}

func (fsu *FileSystemResizeRequest) getRequiredFields() []string {
	return []string{"Uid"}
}

func (fsu *FileSystemResizeRequest) getRelatedObject() ApiObject {
	return &FileSystem{}
}

func (fsu *FileSystemResizeRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(fsu)
}

func (fsu *FileSystemResizeRequest) String() string {
	return fmt.Sprintln("FileSystemResizeRequest(fsUid:", fsu.Uid, "capacity:", fsu.TotalCapacity, ")")
}

type FileSystemDeleteRequest struct {
	Uid uuid.UUID `json:"-"`
}

func (fsd *FileSystemDeleteRequest) String() string {
	return fmt.Sprintln("FileSystemDeleteRequest(fsUid:", fsd.Uid, ")")
}

func (fsd *FileSystemDeleteRequest) getApiUrl(a *ApiClient) string {
	url, err := urlutil.URLJoin(fsd.getRelatedObject().GetBasePath(a), fsd.Uid.String())
	if err != nil {
		return ""
	}
	return url
}

func (fsd *FileSystemDeleteRequest) getRequiredFields() []string {
	return []string{"Uid"}
}

func (fsd *FileSystemDeleteRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(fsd)
}

func (fsd *FileSystemDeleteRequest) getRelatedObject() ApiObject {
	return &FileSystem{}
}

type FileSystemMountTokenRequest struct {
	Uid uuid.UUID `json:"-"`
}

func (fsm *FileSystemMountTokenRequest) String() string {
	return fmt.Sprintln("FilesystemMountTokenRequest(fsUid:", fsm.Uid, ")")
}

func (fsm *FileSystemMountTokenRequest) getApiUrl(a *ApiClient) string {
	url, err := urlutil.URLJoin(fsm.getRelatedObject().GetBasePath(a), fsm.Uid.String(), "mountToken")
	if err != nil {
		return ""
	}
	return url
}

func (fsm *FileSystemMountTokenRequest) getRequiredFields() []string {
	return []string{"Uid"}
}

func (fsm *FileSystemMountTokenRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(fsm)
}

func (fsm *FileSystemMountTokenRequest) getRelatedObject() ApiObject {
	return &FileSystem{}
}
