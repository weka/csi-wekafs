package apiclienttest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// The record types below hold this fake cluster's state. Their exported-looking field names
// mirror apiclient's own wire structs (see apiclient/*.go) purely for readability; they are not
// shared types, since this package must not import apiclient.

type filesystemRecord struct {
	Uid           string
	Name          string
	TotalCapacity int64
	FreeTotal     int64
}

func (f *filesystemRecord) toWire() map[string]any {
	return map[string]any{
		"id":                     f.Uid,
		"name":                   f.Name,
		"uid":                    f.Uid,
		"is_removing":            false,
		"group_id":               "default",
		"is_creating":            false,
		"free_total":             f.FreeTotal,
		"is_encrypted":           false,
		"metadata_budget":        0,
		"used_total_data":        f.TotalCapacity - f.FreeTotal,
		"used_total":             f.TotalCapacity - f.FreeTotal,
		"ssd_budget":             0,
		"is_ready":               true,
		"group_name":             "default",
		"available_total":        f.FreeTotal,
		"status":                 "STATUS_OK",
		"used_ssd_metadata":      0,
		"auth_required":          false,
		"available_ssd_metadata": 0,
		"total_budget":           f.TotalCapacity,
		"used_ssd_data":          0,
		"available_ssd":          f.FreeTotal,
		"free_ssd":               f.FreeTotal,
	}
}

type interfaceGroupRecord struct {
	Uid        string
	Name       string
	SubnetMask string
	Gateway    string
	Ips        []string
	Type       string
	Status     string
}

func (i interfaceGroupRecord) toWire() map[string]any {
	return map[string]any{
		"subnet_mask":       i.SubnetMask,
		"name":              i.Name,
		"uid":               i.Uid,
		"ips":               i.Ips,
		"allow_manage_gids": false,
		"type":              i.Type,
		"gateway":           i.Gateway,
		"status":            i.Status,
	}
}

type nfsPermissionRecord struct {
	Uid               string
	Filesystem        string
	Group             string
	Path              string
	PermissionType    string
	SquashMode        string
	SupportedVersions []string
	AnonUid           string
	AnonGid           string
}

func (p *nfsPermissionRecord) toWire() map[string]any {
	return map[string]any{
		"group_id":           p.Group,
		"privileged_port":    false,
		"supported_versions": p.SupportedVersions,
		"id":                 p.Uid,
		"obs_direct":         false,
		"anon_uid":           p.AnonUid,
		"manage_gids":        false,
		"custom_options":     "",
		"filesystem":         p.Filesystem,
		"uid":                p.Uid,
		"group":              p.Group,
		"NfsClientGroup_id":  "",
		"permission_type":    p.PermissionType,
		"mount_options":      "",
		"path":               p.Path,
		"squash_mode":        p.SquashMode,
		"root_squashing":     false,
		"anon_gid":           p.AnonGid,
		"enable_auth_types":  []string{},
	}
}

type ruleRecord struct {
	Uid  string
	Type string
	Rule string
	Id   string
}

func (r ruleRecord) toWire() map[string]any {
	return map[string]any{
		"type": r.Type,
		"uid":  r.Uid,
		"rule": r.Rule,
		"id":   r.Id,
	}
}

type clientGroupRecord struct {
	Uid   string
	Name  string
	Id    string
	Rules []ruleRecord
}

func (g *clientGroupRecord) toWire() map[string]any {
	rules := make([]map[string]any, 0, len(g.Rules))
	for _, r := range g.Rules {
		rules = append(rules, r.toWire())
	}
	return map[string]any{
		"uid":   g.Uid,
		"rules": rules,
		"id":    g.Id,
		"name":  g.Name,
	}
}

type quotaRecord struct {
	InodeId        uint64
	UsedBytes      uint64
	HardLimitBytes uint64
	SoftLimitBytes uint64
	Status         string
}

// toWire and toWireInList are deliberately different, because the real API's two quota endpoints
// are. Fetching one quota by inode returns used_bytes and no total_bytes; listing a filesystem's
// quotas returns total_bytes and no used_bytes. Serving one shape from both - which this fake used
// to do - makes a client that decodes the wrong key pass every test, which is exactly how the
// per-volume path came to report every volume's used capacity as zero.
func (q *quotaRecord) toWire() map[string]any {
	return map[string]any{
		"inode_id":         q.InodeId,
		"used_bytes":       q.UsedBytes,
		"hard_limit_bytes": q.HardLimitBytes,
		"soft_limit_bytes": q.SoftLimitBytes,
		"snap_view_id":     1,
		"status":           q.Status,
	}
}

func (q *quotaRecord) toWireInList() map[string]any {
	return map[string]any{
		"inode_id":         q.InodeId,
		"total_bytes":      q.UsedBytes,
		"hard_limit_bytes": q.HardLimitBytes,
		"soft_limit_bytes": q.SoftLimitBytes,
		"snap_view_id":     1,
		"status":           q.Status,
	}
}

// writeData wraps v in the envelope every real Weka API response uses: {"data": ...}. ApiClient's
// do()/request() unwrap it the same way on the way back in.
func writeData(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": message})
}

// routes builds the request multiplexer. It uses Go 1.22's method+wildcard ServeMux patterns
// (e.g. "GET /api/v2/fileSystems/{uid}"), so each endpoint is registered once and routed exactly
// like the real API prefix "/api/v2" that apiclient.ApiClient.getBaseUrl always includes.
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v2/login", s.handleLogin)
	mux.HandleFunc("POST /api/v2/login/refresh", s.handleRefresh)
	mux.HandleFunc("GET /api/v2/security/defaultTokensExpiry", s.handleTokenExpiry)
	mux.HandleFunc("GET /api/v2/cluster", s.handleCluster)
	mux.HandleFunc("GET /api/v2/users/whoami", s.handleWhoami)
	mux.HandleFunc("GET /api/v2/nodes", s.handleNodes)
	mux.HandleFunc("GET /api/v2/processes", s.handleNodes)

	mux.HandleFunc("GET /api/v2/fileSystems", s.handleFsList)
	mux.HandleFunc("GET /api/v2/fileSystems/{uid}", s.handleFsGet)
	mux.HandleFunc("GET /api/v2/fileSystems/{uid}/resolvePath", s.handleResolvePath)
	// The client builds the listing URL with a lowercase "filesystems" and the single-quota URL
	// with camelCase "fileSystems"; both are registered as the client spells them.
	mux.HandleFunc("GET /api/v2/filesystems/{uid}/quota", s.handleQuotaList)
	mux.HandleFunc("GET /api/v2/fileSystems/{uid}/quota/{inode}", s.handleQuotaGet)
	mux.HandleFunc("PUT /api/v2/fileSystems/{uid}/quota/{inode}", s.handleQuotaPut)
	mux.HandleFunc("DELETE /api/v2/fileSystems/{uid}/quotas/{inode}", s.handleQuotaDelete)

	mux.HandleFunc("GET /api/v2/interfaceGroups", s.handleInterfaceGroups)

	mux.HandleFunc("GET /api/v2/nfs/permissions", s.handleNfsPermissionsList)
	mux.HandleFunc("POST /api/v2/nfs/permissions", s.handleNfsPermissionCreate)
	mux.HandleFunc("DELETE /api/v2/nfs/permissions/{uid}", s.handleNfsPermissionDelete)

	mux.HandleFunc("GET /api/v2/nfs/clientGroups", s.handleClientGroupsList)
	mux.HandleFunc("POST /api/v2/nfs/clientGroups", s.handleClientGroupCreate)
	mux.HandleFunc("GET /api/v2/nfs/clientGroups/{uid}", s.handleClientGroupGet)
	mux.HandleFunc("DELETE /api/v2/nfs/clientGroups/{uid}", s.handleClientGroupDelete)
	mux.HandleFunc("POST /api/v2/nfs/clientGroups/{uid}/rules", s.handleClientGroupRuleCreate)

	mux.HandleFunc("GET /api/v2/kms", s.handleKms)

	return mux
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	writeData(w, map[string]any{
		"access_token":  "fake-access-token",
		"token_type":    "bearer",
		"expires_in":    3600,
		"refresh_token": "fake-refresh-token",
	})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	writeData(w, map[string]any{
		"access_token":  "fake-access-token-2",
		"token_type":    "bearer",
		"expires_in":    3600,
		"refresh_token": "fake-refresh-token-2",
	})
}

func (s *Server) handleTokenExpiry(w http.ResponseWriter, r *http.Request) {
	writeData(w, map[string]any{
		"access_token_expiry":  3600,
		"refresh_token_expiry": 86400,
	})
}

func (s *Server) handleCluster(w http.ResponseWriter, r *http.Request) {
	writeData(w, map[string]any{
		"name":         "fake-cluster",
		"release_hash": "0000000000000000",
		"init_stage":   "STABLE",
		"release":      s.cfg.clusterVersion,
		"guid":         uuid.New().String(),
		"capacity": map[string]any{
			"total_bytes":         100 << 40,
			"hot_spare_bytes":     0,
			"unprovisioned_bytes": 50 << 40,
		},
	})
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	writeData(w, map[string]any{
		"org_id":   1,
		"username": "admin",
		"source":   "local",
		"uid":      uuid.New().String(),
		"role":     s.cfg.userRole,
		"org_name": "Root",
	})
}

// handleNodes backs both the legacy "nodes" and current "processes" API paths. With
// WithRotatingEndpoints, it returns a different (but always genuinely reachable) set of management
// endpoints on every call, so ApiClient.UpdateApiEndpoints has something real to replace its
// internal endpoint map with - the condition the concurrency race tests need.
func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.rotating {
		writeData(w, []map[string]any{s.nodeWire("node-0", []string{"127.0.0.1"})})
		return
	}

	gen := s.nodeCallCount.Add(1)
	// "127.0.0.1" and "localhost" both resolve back to the loopback address this httptest server
	// listens on, so every combination below stays reachable while genuinely varying the set.
	aliases := []string{"127.0.0.1", "localhost"}
	var ips []string
	switch gen % 3 {
	case 0:
		ips = aliases[:1]
	case 1:
		ips = aliases[1:]
	default:
		ips = aliases
	}
	nodes := make([]map[string]any, 0, len(ips))
	for _, ip := range ips {
		nodes = append(nodes, s.nodeWire(fmt.Sprintf("node-%d", gen), []string{ip}))
	}
	writeData(w, nodes)
}

func (s *Server) nodeWire(id string, ips []string) map[string]any {
	return map[string]any{
		"id":           id,
		"network_mode": "",
		"mode":         "backend",
		"uid":          uuid.New().String(),
		"hostname":     "fake-node",
		"ips":          ips,
		"mgmt_port":    s.mgmtPort,
		"slot":         0,
		"roles":        []string{"MANAGEMENT", "COMPUTE"},
		"status":       "UP",
	}
}

func (s *Server) handleFsList(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	list := make([]map[string]any, 0, len(s.filesystems))
	for _, fs := range s.filesystems {
		list = append(list, fs.toWire())
	}
	s.mu.Unlock()
	writeData(w, list)
}

func (s *Server) handleFsGet(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	s.mu.Lock()
	fs, ok := s.filesystems[uid]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "filesystem not found")
		return
	}
	writeData(w, fs.toWire())
}

// handleResolvePath only needs to satisfy TestApiClient_ResolvePathToInode: "/" on a known
// filesystem resolves, anything else - an unknown path or an unknown filesystem - is a 404, which
// ApiClient.ResolvePathToInode turns into ObjectNotFoundError.
func (s *Server) handleResolvePath(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	s.mu.Lock()
	_, ok := s.filesystems[uid]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "filesystem not found")
		return
	}
	if r.URL.Query().Get("path") != "/" {
		writeError(w, http.StatusNotFound, "path not found")
		return
	}
	writeData(w, map[string]any{"inode_id": "1"})
}

func (s *Server) quotaKey(fsUid string, inodeId string) string {
	return fsUid + ":" + inodeId
}

func (s *Server) handleQuotaGet(w http.ResponseWriter, r *http.Request) {
	key := s.quotaKey(r.PathValue("uid"), r.PathValue("inode"))
	s.mu.Lock()
	q, ok := s.quotas[key]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "quota not found")
		return
	}
	writeData(w, q.toWire())
}

// handleQuotaList serves the filesystem-wide quota listing that batch mode reads. Quotas are keyed
// "<fsUid>:<inode>", so the filesystem's own quotas are the entries whose key carries its uid.
func (s *Server) handleQuotaList(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	s.mu.Lock()
	out := make([]map[string]any, 0, len(s.quotas))
	for key, q := range s.quotas {
		if strings.HasPrefix(key, uid+":") {
			out = append(out, q.toWireInList())
		}
	}
	s.mu.Unlock()
	writeData(w, out)
}

func (s *Server) handleQuotaPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		HardLimitBytes uint64 `json:"hard_limit_bytes"`
		SoftLimitBytes uint64 `json:"soft_limit_bytes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	inodeId, _ := strconv.ParseUint(r.PathValue("inode"), 10, 64)
	q := &quotaRecord{
		InodeId:        inodeId,
		UsedBytes:      0,
		HardLimitBytes: body.HardLimitBytes,
		SoftLimitBytes: body.SoftLimitBytes,
		Status:         "ACTIVE",
	}
	key := s.quotaKey(r.PathValue("uid"), r.PathValue("inode"))
	s.mu.Lock()
	s.quotas[key] = q
	s.mu.Unlock()
	writeData(w, q.toWire())
}

// SetQuotaUsedBytes sets how much capacity a seeded quota has consumed. Quotas created through the
// API start empty, so a test that cares what "used" reports has to say so explicitly.
func (s *Server) SetQuotaUsedBytes(fsUid string, inodeId uint64, used uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if q, ok := s.quotas[s.quotaKey(fsUid, strconv.FormatUint(inodeId, 10))]; ok {
		q.UsedBytes = used
	}
}

func (s *Server) handleQuotaDelete(w http.ResponseWriter, r *http.Request) {
	key := s.quotaKey(r.PathValue("uid"), r.PathValue("inode"))
	s.mu.Lock()
	delete(s.quotas, key)
	s.mu.Unlock()
	writeData(w, map[string]any{})
}

func (s *Server) handleInterfaceGroups(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	list := make([]map[string]any, 0, len(s.interfaceGroups))
	for _, ig := range s.interfaceGroups {
		list = append(list, ig.toWire())
	}
	s.mu.Unlock()
	writeData(w, list)
}

func (s *Server) handleNfsPermissionsList(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	list := make([]map[string]any, 0, len(s.nfsPermissions))
	for _, p := range s.nfsPermissions {
		list = append(list, p.toWire())
	}
	s.mu.Unlock()
	writeData(w, list)
}

func (s *Server) handleNfsPermissionCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Filesystem        string   `json:"filesystem"`
		Group             string   `json:"group"`
		Path              string   `json:"path"`
		PermissionType    string   `json:"permission_type"`
		SquashMode        string   `json:"squash_mode"`
		AnonUid           int      `json:"anon_uid"`
		AnonGid           int      `json:"anon_gid"`
		SupportedVersions []string `json:"supported_versions"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	p := &nfsPermissionRecord{
		Uid:               uuid.New().String(),
		Filesystem:        body.Filesystem,
		Group:             body.Group,
		Path:              body.Path,
		PermissionType:    body.PermissionType,
		SquashMode:        body.SquashMode,
		SupportedVersions: body.SupportedVersions,
		AnonUid:           fmt.Sprintf("%d", body.AnonUid),
		AnonGid:           fmt.Sprintf("%d", body.AnonGid),
	}
	s.mu.Lock()
	s.nfsPermissions[p.Uid] = p
	s.mu.Unlock()
	writeData(w, p.toWire())
}

func (s *Server) handleNfsPermissionDelete(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	s.mu.Lock()
	_, ok := s.nfsPermissions[uid]
	delete(s.nfsPermissions, uid)
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "nfs permission not found")
		return
	}
	writeData(w, map[string]any{})
}

func (s *Server) handleClientGroupsList(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	list := make([]map[string]any, 0, len(s.clientGroups))
	for _, g := range s.clientGroups {
		list = append(list, g.toWire())
	}
	s.mu.Unlock()
	writeData(w, list)
}

func (s *Server) handleClientGroupCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	g := &clientGroupRecord{
		Uid:  uuid.New().String(),
		Name: body.Name,
		Id:   body.Name,
	}
	s.mu.Lock()
	s.clientGroups[g.Uid] = g
	s.mu.Unlock()
	writeData(w, g.toWire())
}

func (s *Server) handleClientGroupGet(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	s.mu.Lock()
	g, ok := s.clientGroups[uid]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "client group not found")
		return
	}
	writeData(w, g.toWire())
}

func (s *Server) handleClientGroupDelete(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	s.mu.Lock()
	_, ok := s.clientGroups[uid]
	delete(s.clientGroups, uid)
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "client group not found")
		return
	}
	writeData(w, map[string]any{})
}

func (s *Server) handleClientGroupRuleCreate(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")

	var body struct {
		Hostname string `json:"dns,omitempty"`
		Ip       string `json:"ip,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	rule := ruleRecord{Uid: uuid.New().String()}
	rule.Id = rule.Uid
	if body.Hostname != "" {
		rule.Type = "DNS"
		rule.Rule = body.Hostname
	} else {
		rule.Type = "IP"
		rule.Rule = body.Ip
	}

	s.mu.Lock()
	g, ok := s.clientGroups[uid]
	if ok {
		g.Rules = append(g.Rules, rule)
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "client group not found")
		return
	}
	writeData(w, rule.toWire())
}

// handleKms always reports "not configured". This must be a 200 with a KMS type that
// Kms.IsSupportedForCommonEncryptionKey() rejects (empty, i.e. neither HashiCorpVault nor KMIP) -
// not an HTTP error - because ApiClient.GetKmsConfiguration only produces its "KMS configuration is
// not supported" error (which TestGetKmsWhenNotDefined asserts on verbatim) when the request itself
// succeeded and the returned type just isn't one it supports.
func (s *Server) handleKms(w http.ResponseWriter, r *http.Request) {
	writeData(w, map[string]any{
		"kms_type": "",
		"params": map[string]any{
			"master_key_name": "",
			"base_url":        "",
			"auth_method":     "",
		},
	})
}
