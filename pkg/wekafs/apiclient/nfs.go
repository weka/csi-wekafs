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
)

type NfsVersionString string
type NfsAuthType string

const (
	NfsPermissionTypeReadWrite                   = "RW"
	NfsPermissionTypeReadOnly                    = "RO"
	NfsPermissionSquashModeNone                  = "none"
	NfsPermissionSquashModeRoot                  = "root"
	NfsPermissionSquashModeAll                   = "all"
	NfsClientGroupRuleTypeDNS                    = "DNS"
	NfsClientGroupRuleTypeIP                     = "IP"
	NfsClientGroupName                           = "WekaCSIPluginClients"
	NfsVersionV3                NfsVersionString = "V3"
	NfsVersionV4                NfsVersionString = "V4"
	NfsAuthTypeNone             NfsAuthType      = "NONE"
	NfsAuthTypeSys              NfsAuthType      = "SYS"
	NfsAuthTypeKerberos5        NfsAuthType      = "KRB5"
)

type NfsPermission struct {
	GroupId           *string             `json:"group_id,omitempty"`
	PrivilegedPort    *bool               `json:"privileged_port,omitempty"`
	SupportedVersions *[]NfsVersionString `json:"supported_versions,omitempty"`
	Id                *string             `json:"id,omitempty"`
	ObsDirect         *bool               `json:"obs_direct,omitempty"`
	AnonUid           *string             `json:"anon_uid,omitempty"`
	ManageGids        *bool               `json:"manage_gids,omitempty"`
	CustomOptions     *string             `json:"custom_options,omitempty"`
	Filesystem        string              `json:"filesystem"`
	Uid               *uuid.UUID          `json:"uid,omitempty"`
	Group             string              `json:"group"`
	NfsClientGroupId  *string             `json:"NfsClientGroup_id,omitempty"`
	PermissionType    string              `json:"permission_type,omitempty"`
	MountOptions      *string             `json:"mount_options,omitempty"`
	Path              string              `json:"path,omitempty"`
	SquashMode        string              `json:"squash_mode,omitempty"`
	RootSquashing     *bool               `json:"root_squashing,omitempty"`
	AnonGid           *string             `json:"anon_gid,omitempty"`
	EnableAuthTypes   []NfsAuthType       `json:"enable_auth_types,omitempty"`
}

func (n *NfsPermission) GetType() string {
	return "nfsPermission"
}

func (n *NfsPermission) GetBasePath(a *ApiClient) string {
	return "nfs/permissions"
}

func (n *NfsPermission) GetApiUrl(a *ApiClient) string {
	url, err := urlutil.URLJoin(n.GetBasePath(a), n.Uid.String())
	if err != nil {
		return url
	}
	return ""
}

func (n *NfsPermission) EQ(other ApiObject) bool {
	return ObjectsAreEqual(n, other)
}

func (n *NfsPermission) getImmutableFields() []string {
	return []string{"Id", "Uid", "NfsClientGroup", "NfsClientGroupId", "GroupId", "Group", "PrivilegedPort", "ManageGids",
		"CustomOptions", "RootSquashing"}
}

func (n *NfsPermission) String() string {
	return fmt.Sprintln("NfsPermission Uid:", n.Uid.String(), "NfsClientGroup:", n.Group, "path:", n.Path)
}

func (n *NfsPermission) IsEligibleForCsi() bool {
	return n.RootSquashing != nil && *n.RootSquashing == false &&
		n.SupportedVersions != nil && slices.Contains(*n.SupportedVersions, "V4") &&
		n.PermissionType == NfsPermissionTypeReadWrite &&
		n.SquashMode == NfsPermissionSquashModeNone
}

func (a *ApiClient) GetNfsPermissions(ctx context.Context, permissions *[]NfsPermission) error {
	n := &NfsPermission{}

	err := a.Get(ctx, n.GetBasePath(a), nil, permissions)
	if err != nil {
		return err
	}
	return nil
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
		if r.EQ(query) {
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
	query := &NfsPermission{Uid: &uid}
	return a.GetNfsPermissionByFilter(ctx, query)
}

type NfsPermissionCreateRequest struct {
	Filesystem        string    `json:"filesystem"`
	Group             string    `json:"group"`
	Path              string    `json:"path"`
	PermissionType    string    `json:"permission_type"`
	SquashMode        string    `json:"squash_mode"`
	AnonUid           int       `json:"anon_uid"`
	AnonGid           int       `json:"anon_gid"`
	ObsDirect         *bool     `json:"obs_direct,omitempty"`
	SupportedVersions *[]string `json:"supported_versions,omitempty"`
	Priority          int       `json:"priority"`
	EnableAuthTypes   []string  `json:"enable_auth_types"`
}

func (qc *NfsPermissionCreateRequest) getApiUrl(a *ApiClient) string {
	return qc.getRelatedObject().GetApiUrl(a)
}
func (qc *NfsPermissionCreateRequest) getRelatedObject() ApiObject {
	return &NfsPermission{
		GroupId: &qc.Group,
	}
}

func (qc *NfsPermissionCreateRequest) getRequiredFields() []string {
	return []string{"filesystem", "group"}
}
func (qc *NfsPermissionCreateRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(qc)
}

func (qc *NfsPermissionCreateRequest) String() string {
	return fmt.Sprintln("NfsPermissionCreateRequest(FS:", qc.Filesystem)
}

func (a *ApiClient) CreateNfsPermission(ctx context.Context, r *NfsPermissionCreateRequest, fs *NfsPermission) error {
	op := "CreateNfsPermission"
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

func EnsureNfsPermission(ctx context.Context, fsName string, group string, apiClient *ApiClient) error {
	perm := &NfsPermission{
		Filesystem:        fsName,
		SupportedVersions: &[]NfsVersionString{NfsVersionV4},
		ObsDirect:         &[]bool{false}[0],
		Group:             group,
		PermissionType:    NfsPermissionTypeReadWrite,
		Path:              "/",
		SquashMode:        NfsPermissionSquashModeNone,
	}
	_, err := apiClient.GetNfsPermissionByFilter(ctx, perm)
	if err == ObjectNotFoundError {
		req := &NfsPermissionCreateRequest{
			Filesystem: fsName,
			Group:      group,
			SquashMode: NfsPermissionSquashModeNone,
		}
		return apiClient.CreateNfsPermission(ctx, req, perm)
	}
	return err
}

type NfsClientGroup struct {
	Uid   *uuid.UUID            `json:"uid,omitempty" url:"-"`
	Rules *[]NfsClientGroupRule `json:"rules,omitempty" url:"-"`
	Id    string                `json:"id" url:"-"`
	Name  string                `json:"name,omitempty" url:"name,omitempty"`
}

func (g *NfsClientGroup) GetType() string {
	return "clientGroup"
}

func (g *NfsClientGroup) GetBasePath(a *ApiClient) string {
	return "nfs/clientGroups"
}

func (g *NfsClientGroup) GetApiUrl(a *ApiClient) string {
	url, err := urlutil.URLJoin(g.GetBasePath(a), g.Uid.String())
	if err != nil {
		return url
	}
	return ""
}

func (g *NfsClientGroup) EQ(other ApiObject) bool {
	return ObjectsAreEqual(g, other)
}

func (g *NfsClientGroup) getImmutableFields() []string {
	return []string{"Name", "Id", "Uid"}
}

func (g *NfsClientGroup) String() string {
	return fmt.Sprintln("NfsClientGroup name:", g.Name, "Uid:", g.Uid.String())
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
	rs := &[]NfsClientGroup{}
	err := a.FindNfsClientGroupsByFilter(ctx, query, rs)
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

func (a *ApiClient) GetNfsClientGroupByUid(ctx context.Context, uid uuid.UUID) (*NfsClientGroup, error) {
	query := &NfsClientGroup{Uid: &uid}
	return a.GetNfsClientGroupByFilter(ctx, query)
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

func (a *ApiClient) EnsureCsiPluginNfsClientGroup(ctx context.Context) (*NfsClientGroup, error) {
	op := "EnsureCsiPluginNfsClientGroup"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	var ret *NfsClientGroup
	ret, err := a.GetNfsClientGroupByName(ctx, NfsClientGroupName)
	if err == nil {
		return ret, nil
	}
	if err != ObjectNotFoundError {
		return ret, err
	}
	err = a.CreateNfsClientGroup(ctx, NewNfsClientGroupCreateRequest(NfsClientGroupName), ret)
	return ret, err
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

type NfsClientGroupRuleType string

type NfsClientGroupRule struct {
	NfsClientGroupUid uuid.UUID              `json:"-" url:"-"`
	Type              NfsClientGroupRuleType `json:"type" url:"-"`
	Uid               uuid.UUID              `json:"uid" url:"-"`
	Rule              string                 `json:"rule" url:"-"`
	Id                string                 `json:"id" url:"-"`
}

func (r *NfsClientGroupRule) GetType() string {
	return "rules"
}

func (r *NfsClientGroupRule) GetBasePath(a *ApiClient) string {
	ncgUrl := (&NfsClientGroup{Uid: &r.Uid}).GetApiUrl(a)
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

func (r *NfsClientGroupRule) getImmutableFields() []string {
	return []string{"Type", "Rule"}
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

func (a *ApiClient) GetNfsClientGroupRules(ctx context.Context, rules *[]NfsClientGroupRule) error {
	op := "GetNfsClientGroupRules"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	cg, err := a.EnsureCsiPluginNfsClientGroup(ctx)
	if err != nil {
		return err
	}
	rules = cg.Rules
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
	cg, err := a.GetNfsClientGroupByUid(ctx, query.NfsClientGroupUid)
	if cg == nil || err != nil {
		return err
	}
	ret := *cg.Rules

	for _, r := range ret {
		if r.EQ(query) {
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
	Type              NfsClientGroupRuleType `json:"type"`
	Hostname          *string                `json:"dns,omitempty"`
	Ip                *string                `json:"ip,omitempty"`
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
	return []string{"NfsClientGroupUid", "Type"}
}

func (r *NfsClientGroupRuleCreateRequest) hasRequiredFields() bool {
	return ObjectRequestHasRequiredFields(r)
}

func (r *NfsClientGroupRuleCreateRequest) getRelatedObject() ApiObject {
	return &NfsClientGroup{Uid: &r.NfsClientGroupUid}
}

func (r *NfsClientGroupRuleCreateRequest) String() string {
	return fmt.Sprintln("NfsClientGroupRuleCreateRequest(NfsClientGroupUid:", r.NfsClientGroupUid, "Type:", r.Type)
}

func NewNfsClientGroupRuleCreateRequest(cgUid uuid.UUID, ruleType NfsClientGroupRuleType, rule string) *NfsClientGroupRuleCreateRequest {

	ret := &NfsClientGroupRuleCreateRequest{
		NfsClientGroupUid: cgUid,
		Type:              ruleType,
	}
	if ruleType == NfsClientGroupRuleTypeDNS {
		ret.Hostname = &rule
	} else if ruleType == NfsClientGroupRuleTypeIP {
		ret.Ip = &rule
	}
	return ret
}

func (a *ApiClient) CreateNfsClientGroupRule(ctx context.Context, r *NfsClientGroupRuleCreateRequest, rule *NfsClientGroupRule) error {
	op := "CreateNfsClientGroupRule"
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

	err = a.Post(ctx, r.getApiUrl(a), &payload, nil, rule)
	return err
}

func (a *ApiClient) EnsureNfsClientGroupRuleForIp(ctx context.Context, cg *NfsClientGroup, ip string) error {
	if cg == nil {
		return errors.New("NfsClientGroup is nil")
	}
	q := &NfsClientGroupRule{Type: NfsClientGroupRuleTypeIP, Rule: ip, NfsClientGroupUid: *cg.Uid}

	rule, err := a.GetNfsClientGroupRuleByFilter(ctx, q)
	if err == ObjectNotFoundError {
		req := NewNfsClientGroupRuleCreateRequest(*cg.Uid, rule.Type, rule.Rule)
		err = a.CreateNfsClientGroupRule(ctx, req, rule)
	}

	return err
}

func (a *ApiClient) EnsureNfsPermissions(ctx context.Context, ip string, fsName string) error {
	op := "EnsureNfsPermissions"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	// Ensure client group
	logger.Debug().Msg("Ensuring CSI Plugin NFS Client Group")
	cg, err := a.EnsureCsiPluginNfsClientGroup(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to ensure NFS client group")
		return err
	}

	// Ensure client group rule
	err = a.EnsureNfsClientGroupRuleForIp(ctx, cg, ip)
	if err != nil {
		return err
	}
	// Ensure NFS permission
	err = EnsureNfsPermission(ctx, fsName, cg.Name, a)
	return err
}
