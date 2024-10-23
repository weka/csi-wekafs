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
	"golang.org/x/exp/slices"
	"k8s.io/helm/pkg/urlutil"
	"strconv"
	"strings"
	"time"
)

type NfsPermissionType string
type NfsPermissionSquashMode string
type NfsClientGroupRuleType string
type NfsVersionString string
type NfsVersionStrings []NfsVersionString

func (n NfsVersionString) String() string {
	return string(n)
}
func (n NfsVersionString) AsOption() string {
	ret := strings.TrimLeft(n.String(), "V")
	if strings.HasPrefix(ret, "3.") {
		ret = "3"
	}
	return ret
}

func (n NfsVersionString) AsWeka() NfsVersionString {
	return NfsVersionString(strings.Split(n.String(), ".")[0])
}

func (s NfsVersionStrings) Matches(other []NfsVersionString) bool {
	for _, v := range s {
		if !slices.Contains(other, v) {
			return false
		}
	}
	return true
}

type NfsAuthType string

//goland:noinspection GoUnusedConst
const (
	NfsPermissionTypeReadWrite  NfsPermissionType       = "RW"
	NfsPermissionTypeReadOnly   NfsPermissionType       = "RO"
	NfsPermissionSquashModeNone NfsPermissionSquashMode = "none"
	NfsPermissionSquashModeRoot NfsPermissionSquashMode = "root"
	NfsPermissionSquashModeAll  NfsPermissionSquashMode = "all"
	NfsClientGroupRuleTypeDNS   NfsClientGroupRuleType  = "DNS"
	NfsClientGroupRuleTypeIP    NfsClientGroupRuleType  = "IP"
	NfsVersionV3                NfsVersionString        = "V3"
	NfsVersionV4                NfsVersionString        = "V4"
	NfsAuthTypeNone             NfsAuthType             = "NONE"
	NfsAuthTypeSys              NfsAuthType             = "SYS"
	NfsAuthTypeKerberos5        NfsAuthType             = "KRB5"
	NfsClientGroupName                                  = "WekaCSIPluginClients"
)

type NfsPermission struct {
	GroupId           string                  `json:"group_id,omitempty" url:"-"`
	PrivilegedPort    bool                    `json:"privileged_port,omitempty" url:"-"`
	SupportedVersions NfsVersionStrings       `json:"supported_versions,omitempty" url:"-"`
	Id                string                  `json:"id,omitempty" url:"-"`
	ObsDirect         bool                    `json:"obs_direct,omitempty" url:"-"`
	AnonUid           string                  `json:"anon_uid,omitempty" url:"-"`
	ManageGids        bool                    `json:"manage_gids,omitempty" url:"-"`
	CustomOptions     string                  `json:"custom_options,omitempty" url:"-"`
	Filesystem        string                  `json:"filesystem" url:"-"`
	Uid               uuid.UUID               `json:"uid,omitempty" url:"-"`
	Group             string                  `json:"group" url:"-"`
	NfsClientGroupId  string                  `json:"NfsClientGroup_id,omitempty" url:"-"`
	PermissionType    NfsPermissionType       `json:"permission_type,omitempty" url:"-"`
	MountOptions      string                  `json:"mount_options,omitempty" url:"-"`
	Path              string                  `json:"path,omitempty" url:"-"`
	SquashMode        NfsPermissionSquashMode `json:"squash_mode,omitempty" url:"-"`
	RootSquashing     bool                    `json:"root_squashing,omitempty" url:"-"`
	AnonGid           string                  `json:"anon_gid,omitempty" url:"-"`
	EnableAuthTypes   []NfsAuthType           `json:"enable_auth_types,omitempty" url:"-"`
}

func (n *NfsPermission) GetType() string {
	return "nfsPermission"
}

//goland:noinspection GoUnusedParameter
func (n *NfsPermission) GetBasePath(a *ApiClient) string {
	return "nfs/permissions"
}

func (n *NfsPermission) GetApiUrl(a *ApiClient) string {
	url, err := urlutil.URLJoin(n.GetBasePath(a), n.Uid.String())
	if err == nil {
		return url
	}
	return ""
}

func (n *NfsPermission) EQ(other ApiObject) bool {
	return ObjectsAreEqual(n, other)
}

func (n *NfsPermission) Matches(o NfsPermission) bool {
	if n.SquashMode == o.SquashMode &&
		n.Filesystem == o.Filesystem &&
		n.Group == o.Group &&
		n.Path == o.Path &&
		n.PermissionType == o.PermissionType &&
		n.SupportedVersions.Matches(o.SupportedVersions) {
		return true
	}
	return false
}

func (n *NfsPermission) getImmutableFields() []string {
	return []string{"Group", "Filesystem", "SupportedVersions", "PermissionType", "Path", "SquashMode"}
}

func (n *NfsPermission) String() string {
	return fmt.Sprintln("NfsPermission Uid:", n.Uid.String(), "NfsClientGroup:", n.Group, "path:", n.Path)
}

func (n *NfsPermission) IsEligibleForCsi() bool {
	return n.RootSquashing == false && slices.Contains(n.SupportedVersions, "V4") &&
		n.PermissionType == NfsPermissionTypeReadWrite &&
		n.SquashMode == NfsPermissionSquashModeNone
}

func (a *ApiClient) FindNfsPermissionsByFilter(ctx context.Context, query *NfsPermission, resultSet *[]NfsPermission) error {
	op := "FindNfsPermissionsByFilter"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	ret := &[]NfsPermission{}
	q, _ := qs.Values(query)
	err := a.Get(ctx, query.GetBasePath(a), q, ret)
	if err != nil {
		return err
	}
	for _, r := range *ret {
		if r.EQ(query) || query.Matches(r) {
			*resultSet = append(*resultSet, r)
		}
	}
	return nil
}

func (a *ApiClient) FindNfsPermissionsByFilesystem(ctx context.Context, fsName string, resultSet *[]NfsPermission) error {
	op := "FindNfsPermissionsByFilter"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	ret := &[]NfsPermission{}
	query := &NfsPermission{Filesystem: fsName}
	q, _ := qs.Values(query)
	err := a.Get(ctx, query.GetBasePath(a), q, ret)
	if err != nil {
		return err
	}
	for _, r := range *ret {
		if r.Filesystem == query.Filesystem {
			*resultSet = append(*resultSet, r)
		}
	}
	return nil
}

// GetNfsPermissionByFilter expected to return exactly one result of FindNfsPermissionsByFilter (error)
func (a *ApiClient) GetNfsPermissionByFilter(ctx context.Context, query *NfsPermission) (*NfsPermission, error) {
	rs := &[]NfsPermission{}
	err := a.FindNfsPermissionsByFilter(ctx, query, rs)
	if err != nil {
		return &NfsPermission{}, err
	}
	if *rs == nil || len(*rs) == 0 {
		return &NfsPermission{}, ObjectNotFoundError
	}
	if len(*rs) > 1 {
		return &NfsPermission{}, MultipleObjectsFoundError
	}
	result := &(*rs)[0]
	return result, nil
}

func (a *ApiClient) GetNfsPermissionsByFilesystemName(ctx context.Context, fsName string, permissions *[]NfsPermission) error {
	query := &NfsPermission{Path: fsName}
	return a.FindNfsPermissionsByFilter(ctx, query, permissions)
}

func (a *ApiClient) GetNfsPermissionByUid(ctx context.Context, uid uuid.UUID) (*NfsPermission, error) {
	query := &NfsPermission{Uid: uid}
	return a.GetNfsPermissionByFilter(ctx, query)
}

type NfsPermissionCreateRequest struct {
	Filesystem        string                  `json:"filesystem"`
	Group             string                  `json:"group"`
	Path              string                  `json:"path"`
	PermissionType    NfsPermissionType       `json:"permission_type"`
	SquashMode        NfsPermissionSquashMode `json:"squash_mode"`
	AnonUid           int                     `json:"anon_uid"`
	AnonGid           int                     `json:"anon_gid"`
	ObsDirect         *bool                   `json:"obs_direct,omitempty"`
	SupportedVersions *[]string               `json:"supported_versions,omitempty"`
	Priority          int                     `json:"priority"`
}

func (qc *NfsPermissionCreateRequest) getApiUrl(a *ApiClient) string {
	return qc.getRelatedObject().GetApiUrl(a)
}
func (qc *NfsPermissionCreateRequest) getRelatedObject() ApiObject {
	return &NfsPermission{
		GroupId: qc.Group,
	}
}

func (qc *NfsPermissionCreateRequest) getRequiredFields() []string {
	return []string{"Filesystem", "Group", "Path", "PermissionType", "SquashMode", "SupportedVersions"}
}
func (qc *NfsPermissionCreateRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(qc)
}

func (qc *NfsPermissionCreateRequest) String() string {
	return fmt.Sprintln("NfsPermissionCreateRequest(FS:", qc.Filesystem)
}

func (a *ApiClient) CreateNfsPermission(ctx context.Context, r *NfsPermissionCreateRequest, p *NfsPermission) error {
	op := "CreateNfsPermission"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx).With().Str("nfs_permission", r.String()).Logger()
	if !r.hasRequiredFields() {
		return RequestMissingParams
	}
	payload, err := json.Marshal(r)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to marshal request")
		return err
	}
	logger.Trace().Msg("Creating permission")
	err = a.Post(ctx, r.getRelatedObject().GetBasePath(a), &payload, nil, p)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to create NFS permission")
		return err
	}
	return nil
}

func EnsureNfsPermission(ctx context.Context, fsName string, group string, version NfsVersionString, apiClient *ApiClient) (created bool, err error) {
	perm := &NfsPermission{
		SupportedVersions: NfsVersionStrings{version.AsWeka()},
		AnonUid:           strconv.Itoa(65534),
		AnonGid:           strconv.Itoa(65534),
		Filesystem:        fsName,
		Group:             group,
		PermissionType:    NfsPermissionTypeReadWrite,
		Path:              "/",
		SquashMode:        NfsPermissionSquashModeNone,
	}
	_, err = apiClient.GetNfsPermissionByFilter(ctx, perm)

	if err != nil {
		if err == ObjectNotFoundError {
			req := &NfsPermissionCreateRequest{
				Filesystem:        fsName,
				Group:             group,
				Path:              "/",
				PermissionType:    NfsPermissionTypeReadWrite,
				SquashMode:        NfsPermissionSquashModeNone,
				AnonGid:           65534,
				AnonUid:           65534,
				SupportedVersions: &[]string{NfsVersionV3.String(), NfsVersionV4.String()},
			}
			err := apiClient.CreateNfsPermission(ctx, req, perm)
			return err == nil, err
		}
	}
	return false, err
}

type NfsPermissionDeleteRequest struct {
	Uid uuid.UUID `json:"-"`
}

func (pd *NfsPermissionDeleteRequest) getApiUrl(a *ApiClient) string {
	return pd.getRelatedObject().GetApiUrl(a)
}

func (pd *NfsPermissionDeleteRequest) getRelatedObject() ApiObject {
	return &NfsPermission{Uid: pd.Uid}
}

func (pd *NfsPermissionDeleteRequest) getRequiredFields() []string {
	return []string{"Uid"}
}

func (pd *NfsPermissionDeleteRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(pd)
}

func (pd *NfsPermissionDeleteRequest) String() string {
	return fmt.Sprintln("NfsPermissionDeleteRequest(uid:", pd.Uid)
}

func (a *ApiClient) DeleteNfsPermission(ctx context.Context, r *NfsPermissionDeleteRequest) error {
	op := "DeleteNfsPermission"
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
				if c == "PermissionDoesNotExistException" {
					return ObjectNotFoundError
				}
			}
		}
	}
	time.Sleep(5 * time.Second) // wait for the permission to be removed
	return nil
}

type NfsClientGroup struct {
	Uid   uuid.UUID            `json:"uid,omitempty" url:"-"`
	Rules []NfsClientGroupRule `json:"rules,omitempty" url:"-"`
	Id    string               `json:"id,omitempty" url:"-"`
	Name  string               `json:"name,omitempty" url:"name,omitempty"`
}

func (g *NfsClientGroup) GetType() string {
	return "clientGroup"
}

//goland:noinspection GoUnusedParameter
func (g *NfsClientGroup) GetBasePath(a *ApiClient) string {
	return "nfs/clientGroups"
}

func (g *NfsClientGroup) GetApiUrl(a *ApiClient) string {
	url, err := urlutil.URLJoin(g.GetBasePath(a), g.Uid.String())
	if err == nil {
		return url
	}
	return ""
}

func (g *NfsClientGroup) EQ(other ApiObject) bool {
	return ObjectsAreEqual(g, other)
}

func (g *NfsClientGroup) getImmutableFields() []string {
	return []string{"Name"}
}

func (g *NfsClientGroup) String() string {
	return fmt.Sprintln("NfsClientGroup name:", g.Name)
}

func (a *ApiClient) GetNfsClientGroups(ctx context.Context, clientGroups *[]NfsClientGroup) error {
	cg := &NfsClientGroup{}

	err := a.Get(ctx, cg.GetBasePath(a), nil, clientGroups)
	if err != nil {
		return err
	}
	return nil
}

func (a *ApiClient) FindNfsClientGroupsByFilter(ctx context.Context, query *NfsClientGroup, resultSet *[]NfsClientGroup) error {
	op := "FindNfsClientGroupsByFilter"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	logger.Trace().Str("client_group_query", query.String()).Msg("Finding client groups by filter")
	ret := &[]NfsClientGroup{}
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

// GetNfsClientGroupByFilter expected to return exactly one result of FindNfsClientGroupsByFilter (error)
func (a *ApiClient) GetNfsClientGroupByFilter(ctx context.Context, query *NfsClientGroup) (*NfsClientGroup, error) {
	op := "GetNfsClientGroupByFilter"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	rs := &[]NfsClientGroup{}
	err := a.FindNfsClientGroupsByFilter(ctx, query, rs)
	logger.Trace().Str("client_group", query.String()).Msg("Getting client group by filter")
	if err != nil {
		return &NfsClientGroup{}, err
	}
	if *rs == nil || len(*rs) == 0 {
		return &NfsClientGroup{}, ObjectNotFoundError
	}
	if len(*rs) > 1 {
		return &NfsClientGroup{}, MultipleObjectsFoundError
	}
	result := &(*rs)[0]
	return result, nil
}

func (a *ApiClient) GetNfsClientGroupByName(ctx context.Context, name string) (*NfsClientGroup, error) {
	query := &NfsClientGroup{Name: name}
	return a.GetNfsClientGroupByFilter(ctx, query)
}

func (a *ApiClient) GetNfsClientGroupByUid(ctx context.Context, uid uuid.UUID, cg *NfsClientGroup) error {
	ret := &NfsClientGroup{
		Uid: uid,
	}
	err := a.Get(ctx, ret.GetApiUrl(a), nil, cg)
	if err != nil {
		switch t := err.(type) {
		case *ApiNotFoundError:
			return ObjectNotFoundError
		case *ApiBadRequestError:
			for _, c := range t.ApiResponse.ErrorCodes {
				if c == "ClientGroupDoesNotExistException" {
					return ObjectNotFoundError
				}
			}
		default:
			return err
		}
	}
	return nil

}

func (a *ApiClient) CreateNfsClientGroup(ctx context.Context, r *NfsClientGroupCreateRequest, fs *NfsClientGroup) error {
	op := "CreateNfsClientGroup"
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
	return err
}

func (a *ApiClient) EnsureCsiPluginNfsClientGroup(ctx context.Context, clientGroupName string) (grp *NfsClientGroup, created bool, err error) {
	op := "EnsureCsiPluginNfsClientGroup"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	var ret *NfsClientGroup
	if clientGroupName == "" {
		clientGroupName = NfsClientGroupName
	}
	logger.Trace().Str("client_group_name", clientGroupName).Msg("Getting client group by name")
	ret, err = a.GetNfsClientGroupByName(ctx, clientGroupName)
	if err != nil {
		if err != ObjectNotFoundError {
			logger.Error().Err(err).Msg("Failed to get client group by name")
			return ret, false, err
		} else {
			logger.Trace().Str("client_group_name", clientGroupName).Msg("Existing client group not found, creating client group")

			err = a.CreateNfsClientGroup(ctx, NewNfsClientGroupCreateRequest(clientGroupName), ret)
			return ret, err == nil, nil
		}
	}
	return ret, false, nil
}

type NfsClientGroupCreateRequest struct {
	Name string `json:"name"`
}

func (fsc *NfsClientGroupCreateRequest) getApiUrl(a *ApiClient) string {
	return fsc.getRelatedObject().GetBasePath(a)
}

func (fsc *NfsClientGroupCreateRequest) getRequiredFields() []string {
	return []string{"Name"}
}

func (fsc *NfsClientGroupCreateRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(fsc)
}
func (fsc *NfsClientGroupCreateRequest) getRelatedObject() ApiObject {
	return &NfsClientGroup{}
}

func (fsc *NfsClientGroupCreateRequest) String() string {
	return fmt.Sprintln("NfsClientGroupCreateRequest(name:", fsc.Name)
}

func NewNfsClientGroupCreateRequest(name string) *NfsClientGroupCreateRequest {
	return &NfsClientGroupCreateRequest{
		Name: name,
	}
}

type NfsClientGroupDeleteRequest struct {
	Uid uuid.UUID `json:"-"`
}

func (cgd *NfsClientGroupDeleteRequest) getApiUrl(a *ApiClient) string {
	return cgd.getRelatedObject().GetApiUrl(a)
}

func (cgd *NfsClientGroupDeleteRequest) getRelatedObject() ApiObject {
	return &NfsClientGroup{Uid: cgd.Uid}
}

func (cgd *NfsClientGroupDeleteRequest) getRequiredFields() []string {
	return []string{"Uid"}
}

func (cgd *NfsClientGroupDeleteRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(cgd)
}

func (cgd *NfsClientGroupDeleteRequest) String() string {
	return fmt.Sprintln("NfsClientGroupDeleteRequest(uid:", cgd.Uid)
}

func (a *ApiClient) DeleteNfsClientGroup(ctx context.Context, r *NfsClientGroupDeleteRequest) error {
	op := "DeleteNfsClientGroup"
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

type NfsClientGroupRule struct {
	NfsClientGroupUid uuid.UUID              `json:"-" url:"-"`
	Type              NfsClientGroupRuleType `json:"type,omitempty" url:"-"`
	Uid               uuid.UUID              `json:"uid,omitempty" url:"-"`
	Rule              string                 `json:"rule,omitempty" url:"-"`
	Id                string                 `json:"id,omitempty" url:"-"`
}

func (r *NfsClientGroupRule) GetType() string {
	return "rules"
}

func (r *NfsClientGroupRule) GetBasePath(a *ApiClient) string {
	ncgUrl := (&NfsClientGroup{Uid: r.Uid}).GetApiUrl(a)
	url, err := urlutil.URLJoin(ncgUrl, r.GetType())
	if err != nil {
		return ""
	}
	return url
}

func (r *NfsClientGroupRule) GetApiUrl(a *ApiClient) string {
	url, err := urlutil.URLJoin(r.GetBasePath(a), r.Uid.String())
	if err != nil {
		return url
	}
	return ""
}

func (r *NfsClientGroupRule) EQ(other ApiObject) bool {
	return ObjectsAreEqual(r, other)
}

func (r *NfsClientGroupRule) IsSupersetOf(other *NfsClientGroupRule) bool {
	if r.IsIPRule() && other.IsIPRule() {
		n1 := r.GetNetwork()
		n2 := other.GetNetwork()
		return n1.ContainsIPAddress(n2.IP.String())
	}
	return false
}

func (r *NfsClientGroupRule) getImmutableFields() []string {
	return []string{"Rule"}
}

func (r *NfsClientGroupRule) String() string {
	return fmt.Sprintln("NfsClientGroupRule Uid:", r.Uid.String(), "clientGroupUid:", r.NfsClientGroupUid.String(),
		"type:", r.Type, "rule", r.Rule)
}

func (r *NfsClientGroupRule) IsIPRule() bool {
	return r.Type == NfsClientGroupRuleTypeIP
}

func (r *NfsClientGroupRule) IsDNSRule() bool {
	return r.Type == NfsClientGroupRuleTypeDNS
}

func (r *NfsClientGroupRule) GetNetwork() *Network {
	if !r.IsIPRule() {
		return nil
	}
	n, err := parseNetworkString(r.Rule)
	if err != nil {
		return nil
	}
	return n
}

func (r *NfsClientGroupRule) IsEligibleForIP(ip string) bool {
	network := r.GetNetwork()
	if network == nil {
		return false
	}
	return network.ContainsIPAddress(ip)
}

func (a *ApiClient) GetNfsClientGroupRules(ctx context.Context, clientGroupName string, rules *[]NfsClientGroupRule) error {
	op := "GetNfsClientGroupRules"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	cg, _, err := a.EnsureCsiPluginNfsClientGroup(ctx, clientGroupName)
	if err != nil {
		return err
	}
	copiedRules := make([]NfsClientGroupRule, len(cg.Rules))
	copy(copiedRules, cg.Rules)
	*rules = copiedRules
	return nil
}

func (a *ApiClient) FindNfsClientGroupRulesByFilter(ctx context.Context, query *NfsClientGroupRule, resultSet *[]NfsClientGroupRule) error {
	op := "FindNfsClientGroupRulesByFilter"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)

	// this is different that in other functions since we don't have /rules entry point for GET
	// so we need to get the client group first
	logger.Trace().Str("client_group_uid", query.NfsClientGroupUid.String()).Msg("Getting client group")
	cg := &NfsClientGroup{}
	err := a.GetNfsClientGroupByUid(ctx, query.NfsClientGroupUid, cg)
	if cg == nil || err != nil {
		return err
	}
	ret := cg.Rules

	for _, r := range ret {
		if r.EQ(query) {
			*resultSet = append(*resultSet, r)
		} else if r.IsSupersetOf(query) {
			// if we have a rule that covers the IP address by bigger network segment, also add it
			*resultSet = append(*resultSet, r)
		}
	}
	return nil
}

func (a *ApiClient) GetNfsClientGroupRuleByFilter(ctx context.Context, rule *NfsClientGroupRule) (*NfsClientGroupRule, error) {
	op := "GetNfsClientGroupRuleByFilter"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	rs := &[]NfsClientGroupRule{}
	err := a.FindNfsClientGroupRulesByFilter(ctx, rule, rs)
	if err != nil {
		return &NfsClientGroupRule{}, err
	}
	if *rs == nil || len(*rs) == 0 {
		return &NfsClientGroupRule{}, ObjectNotFoundError
	}
	if len(*rs) > 1 {
		return &NfsClientGroupRule{}, MultipleObjectsFoundError
	}
	result := &(*rs)[0]
	return result, nil
}

type NfsClientGroupRuleCreateRequest struct {
	NfsClientGroupUid uuid.UUID              `json:"-"`
	Type              NfsClientGroupRuleType `json:"-"`
	Hostname          string                 `json:"dns,omitempty"`
	Ip                string                 `json:"ip,omitempty"`
}

func (r *NfsClientGroupRuleCreateRequest) getType() string {
	return "rules"
}

func (r *NfsClientGroupRuleCreateRequest) getApiUrl(a *ApiClient) string {
	ret, err := urlutil.URLJoin(r.getRelatedObject().GetApiUrl(a), r.getType())
	if err != nil {
		return ""
	}
	return ret
}

func (r *NfsClientGroupRuleCreateRequest) getRequiredFields() []string {
	return []string{"Type"}
}

func (r *NfsClientGroupRuleCreateRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(r)
}

func (r *NfsClientGroupRuleCreateRequest) getRelatedObject() ApiObject {
	return &NfsClientGroup{Uid: r.NfsClientGroupUid}
}

func (r *NfsClientGroupRuleCreateRequest) String() string {
	return fmt.Sprintln("NfsClientGroupRuleCreateRequest(NfsClientGroupUid:", r.NfsClientGroupUid, "Type:", r.Type)
}

func (r *NfsClientGroupRuleCreateRequest) AsRule() string {
	if r.Type == NfsClientGroupRuleTypeDNS {
		return r.Hostname
	}
	return r.Ip
}

func NewNfsClientGroupRuleCreateRequest(cgUid uuid.UUID, ruleType NfsClientGroupRuleType, rule string) *NfsClientGroupRuleCreateRequest {

	ret := &NfsClientGroupRuleCreateRequest{
		NfsClientGroupUid: cgUid,
		Type:              ruleType,
	}
	if ruleType == NfsClientGroupRuleTypeDNS {
		ret.Hostname = rule
	} else if ruleType == NfsClientGroupRuleTypeIP {
		net, err := parseNetworkString(rule)
		if err != nil {
			return nil
		}
		ret.Ip = net.AsNfsRule()
	}
	return ret
}

func (a *ApiClient) CreateNfsClientGroupRule(ctx context.Context, r *NfsClientGroupRuleCreateRequest, rule *NfsClientGroupRule) error {
	op := "CreateNfsClientGroupRule"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	logger.Trace().Str("client_group_rule", r.String()).Msg("Creating client group rule")

	if !r.hasRequiredFields() {
		return RequestMissingParams
	}

	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}

	err = a.Post(ctx, r.getApiUrl(a), &payload, nil, rule)
	return err
}

func (a *ApiClient) EnsureNfsClientGroupRuleForIp(ctx context.Context, cg *NfsClientGroup, ip string) (created bool, err error) {
	if cg == nil {
		return false, errors.New("NfsClientGroup is nil")
	}
	r, err := parseNetworkString(ip)
	if err != nil {
		return false, err
	}

	q := &NfsClientGroupRule{Type: NfsClientGroupRuleTypeIP, Rule: r.AsNfsRule(), NfsClientGroupUid: cg.Uid}

	rule, err := a.GetNfsClientGroupRuleByFilter(ctx, q)
	if err != nil {
		if err == ObjectNotFoundError {
			req := NewNfsClientGroupRuleCreateRequest(cg.Uid, q.Type, q.Rule)
			err = a.CreateNfsClientGroupRule(ctx, req, rule)
			return err == nil, err
		}
	}
	return false, err
}

func (a *ApiClient) EnsureNfsPermissions(ctx context.Context, ip string, fsName string, version NfsVersionString, clientGroupName string) error {
	op := "EnsureNfsPermissions"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	updateConfigRequired := false
	logger := log.Ctx(ctx).With().Bool("update_config_required", func() bool { return updateConfigRequired }()).Logger()
	clientGroupCaption := clientGroupName
	if clientGroupCaption == "" {
		clientGroupCaption = NfsClientGroupName
	}
	var created bool

	logger.Debug().Str("ip", ip).Str("filesystem", fsName).Str("client_group_name", clientGroupCaption).Msg("Ensuring NFS permissions")
	// Ensure client group
	logger.Trace().Msg("Ensuring CSI Plugin NFS Client Group")
	cg, created, err := a.EnsureCsiPluginNfsClientGroup(ctx, clientGroupName)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to ensure NFS client group")
		return err
	}
	updateConfigRequired = updateConfigRequired || created

	// Ensure client group rule
	logger.Trace().Str("ip_address", ip).Msg("Ensuring NFS Client Group Rule for IP")
	created, err = a.EnsureNfsClientGroupRuleForIp(ctx, cg, ip)
	if err != nil {
		logger.Error().Err(err).Str("ip_address", ip).Msg("Failed to ensure NFS client group rule for IP")
		return err
	}
	updateConfigRequired = updateConfigRequired || created
	// Ensure NFS permission
	logger.Trace().Str("filesystem", fsName).Str("client_group", cg.Name).Msg("Ensuring NFS Export for client group")
	created, err = EnsureNfsPermission(ctx, fsName, cg.Name, version, a)
	updateConfigRequired = updateConfigRequired || created
	if updateConfigRequired {
		logger.Trace().Msg("Waiting for NFS configuration to be applied")
		time.Sleep(5 * time.Second)
	}
	return err
}
