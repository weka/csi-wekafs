package apiclient

import (
	"encoding/json"
	"github.com/google/uuid"
	"k8s.io/helm/pkg/urlutil"
)

type FileSystem struct {
	Id                   string    `json:"id"`
	AutoMaxFiles         bool      `json:"auto_max_files"`
	UsedSsdData          int64     `json:"used_ssd_data"`
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

func (a *ApiClient) GetFileSystemByUid(uid uuid.UUID, fs *FileSystem) error {
	ret := &FileSystem{
		Uid: uid,
	}
	return a.Get(ret.GetApiUrl(), nil, fs)
}

func (a *ApiClient) GetFileSystemsByFilter(query *FileSystem, resultSet *[]FileSystem) error {
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

func (a *ApiClient) CreateFileSystem(r *FileSystemCreateRequest, fs *FileSystem) error {
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

func (a *ApiClient) UpdateFileSystem(r *FileSystemUpdateRequest, fs *FileSystem) error {
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

func (f *FileSystem) GetType() string {
	return "filesystem"
}

func (f *FileSystem) GetBasePath() string {
	return "fileSystems"
}

func (f *FileSystem) GetApiUrl() string {
	url, err := urlutil.URLJoin(f.GetBasePath(), f.Uid.String())
	if err != nil {
		return url
	}
	return ""
}

func (f *FileSystem) getImmutableFields() []string {
	return []string{
		"Name",
		"TotalCapacity",
		"GroupName",
	}
}

func (f *FileSystem) EQ(q ApiObject) bool {
	return ObjectsAreEqual(q, f)
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

type FileSystemUpdateRequest struct {
	Uid           uuid.UUID `json:"-"`
	NewName       string    `json:"new_name,omitempty"`
	TotalCapacity int64     `json:"total_capacity,omitempty"`
	SsdCapacity   int64     `json:"ssd_capacity,omitempty"`
	AuthRequired  bool      `json:"auth_required,omitempty"`
}

func (fsu *FileSystemUpdateRequest) getApiUrl() string {
	url, err := urlutil.URLJoin(fsu.getRelatedObject().GetBasePath(), fsu.Uid.String())
	if err != nil {
		return ""
	}
	return url
}

func (fsu *FileSystemUpdateRequest) getRequiredFields() []string {
	return []string{"Uid"}
}

func (fsu *FileSystemUpdateRequest) getRelatedObject() ApiObject {
	return &FileSystem{}
}

func (fsu *FileSystemUpdateRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(fsu)
}

type FileSystemDeleteRequest struct {
	Uid uuid.UUID `json:"-"`
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
