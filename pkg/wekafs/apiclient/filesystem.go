package apiclient

import (
	"encoding/json"
	"fmt"
	"github.com/golang/glog"
	"github.com/google/uuid"
	"k8s.io/helm/pkg/urlutil"
)

type FileSystem struct {
	Id                   string    `json:"id"`
	Name                 string    `json:"name"`
	Uid                  uuid.UUID `json:"uid"`
	IsRemoving           bool      `json:"is_removing"`
	GroupId              string    `json:"group_id"`
	IsCreating           bool      `json:"is_creating"`
	FreeTotal            int64     `json:"free_total"`
	IsEncrypted          bool      `json:"is_encrypted"`
	MetadataBudget       int64     `json:"metadata_budget"`
	UsedTotalData        int64     `json:"used_total_data"`
	UsedTotal            int64     `json:"used_total"`
	SsdBudget            int64     `json:"ssd_budget"`
	IsReady              bool      `json:"is_ready"`
	GroupName            string    `json:"group_name"`
	AvailableTotal       int64     `json:"available_total"`
	Status               string    `json:"status"`
	UsedSsdMetadata      int64     `json:"used_ssd_metadata"`
	AuthRequired         bool      `json:"auth_required"`
	AvailableSsdMetadata int64     `json:"available_ssd_metadata"`
	TotalCapacity        int64     `json:"total_budget"`
	UsedSsd              int64     `json:"used_ssd"`
	AvailableSsd         int64     `json:"available_ssd"`
	FreeSsd              int64     `json:"free_ssd"`

	MaxFiles       int64         `json:"max_files"`
	ObsBuckets     []interface{} `json:"obs_buckets"`
	ObjectStorages []interface{} `json:"object_storages"`
}

type FileSystemMountToken struct {
	Token          string `json:"mount_token,omitempty"`
	FilesystemName string `json:"filesystem_name,omitempty"`
}

func (fs *FileSystem) String() string {
	return fmt.Sprintln("FileSystem(fsUid:", fs.Uid, "name:", fs.Name, "capacity:", fs.TotalCapacity, ")")
}

func (a *ApiClient) GetFileSystemByUid(uid uuid.UUID, fs *FileSystem) error {
	ret := &FileSystem{
		Uid: uid,
	}
	return a.Get(ret.GetApiUrl(), nil, fs)
}

// FindFileSystemsByFilter returns result set of 0-many objects matching filter
func (a *ApiClient) FindFileSystemsByFilter(query *FileSystem, resultSet *[]FileSystem) error {
	ret := &[]FileSystem{}
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

// GetFileSystemByFilter expected to return exactly one result of FindFileSystemsByFilter (error)
func (a *ApiClient) GetFileSystemByFilter(query *FileSystem) (*FileSystem, error) {
	rs := &[]FileSystem{}
	err := a.FindFileSystemsByFilter(query, rs)
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
	return result, nil
}

func (a *ApiClient) GetFileSystemByName(name string) (*FileSystem, error) {
	query := &FileSystem{Name: name}
	return a.GetFileSystemByFilter(query)
}

func (a *ApiClient) CreateFileSystem(r *FileSystemCreateRequest, fs *FileSystem) error {
	f := a.Log(3, "Creating filesystem", r)
	defer f()
	if !r.hasRequiredFields() {
		return RequestMissingParams
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}

	err = a.Post(r.getRelatedObject().GetBasePath(), &payload, nil, fs)
	if err != nil {
		return err
	}
	return nil
}

func (a *ApiClient) UpdateFileSystem(r *FileSystemResizeRequest, fs *FileSystem) error {
	f := a.Log(3, "Updating filesystem", r)
	defer f()
	if !r.hasRequiredFields() {
		return RequestMissingParams
	}
	var payload []byte
	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}
	err = a.Put(r.getApiUrl(), &payload, nil, fs)
	if err != nil {
		return err
	}
	return nil
}

func (a *ApiClient) DeleteFileSystem(r *FileSystemDeleteRequest) error {
	f := a.Log(3, "Deleting filesystem", r)
	defer f()
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

func (a *ApiClient) GetFileSystemMountToken(r *FileSystemMountTokenRequest, token *FileSystemMountToken) error {
	f := a.Log(5, "Getting a mount token for filesystem", r.Uid)
	defer f()
	if !r.hasRequiredFields() {
		return RequestMissingParams
	}
	err := a.Get(r.getApiUrl(), nil, token)
	if err != nil {
		return err
	}
	glog.V(6).Infoln("Fetched token for filesystem UID", r.Uid, "name", token.FilesystemName, "token", token.Token)
	return nil
}

func (fs *FileSystem) GetType() string {
	return "filesystem"
}

func (fs *FileSystem) GetBasePath() string {
	return "fileSystems"
}

func (fs *FileSystem) GetApiUrl() string {
	url, err := urlutil.URLJoin(fs.GetBasePath(), fs.Uid.String())
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
	SsdCapacity   int64  `json:"ssd_capacity,omitempty"`
	Encrypted     bool   `json:"encrypted,omitempty"`
	AuthRequired  bool   `json:"auth_required,omitempty"`
	AllowNoKms    bool   `json:"allow_no_kms,omitempty"`
}

func (fsc *FileSystemCreateRequest) getApiUrl() string {
	return fsc.getRelatedObject().GetBasePath()
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
	return fmt.Sprintln("FileSystem(name:", fsc.Name, "capacity:", fsc.TotalCapacity, ")")
}

func NewFilesystemCreateRequest(name, groupName string, totalCapacity int64) (*FileSystemCreateRequest, error) {
	ret := &FileSystemCreateRequest{
		Name:          name,
		GroupName:     groupName,
		TotalCapacity: totalCapacity,
	}
	return ret, nil
}

type FileSystemResizeRequest struct {
	Uid           uuid.UUID `json:"-"`
	TotalCapacity *int64    `json:"total_capacity,omitempty"`
	SsdCapacity   *int64    `json:"ssd_capacity,omitempty"`
}

func NewFileSystemResizeRequest(fsUid uuid.UUID, totalCapacity, ssdCapacity *int64) *FileSystemResizeRequest {
	ret := &FileSystemResizeRequest{
		Uid: fsUid,
	}
	if totalCapacity != nil {
		ret.TotalCapacity = totalCapacity
	}
	if ssdCapacity != nil {
		ret.SsdCapacity = ssdCapacity
	}
	return ret
}

func (fsu *FileSystemResizeRequest) getApiUrl() string {
	url, err := urlutil.URLJoin(fsu.getRelatedObject().GetBasePath(), fsu.Uid.String())
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
	return fmt.Sprintln("FileSystem(fsUid:", fsu.Uid, "capacity:", fsu.TotalCapacity, ")")
}

type FileSystemDeleteRequest struct {
	Uid uuid.UUID `json:"-"`
}

func (fsd *FileSystemDeleteRequest) String() string {
	return fmt.Sprintln("FileSystemDeleteRequest(fsUid:", fsd.Uid, ")")
}

func (fsd *FileSystemDeleteRequest) getApiUrl() string {
	url, err := urlutil.URLJoin(fsd.getRelatedObject().GetBasePath(), fsd.Uid.String())
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

func (fsm *FileSystemMountTokenRequest) getApiUrl() string {
	url, err := urlutil.URLJoin(fsm.getRelatedObject().GetBasePath(), fsm.Uid.String(), "mountToken")
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
