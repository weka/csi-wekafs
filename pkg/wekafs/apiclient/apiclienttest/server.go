// Package apiclienttest provides a stateful, in-memory fake of the Weka REST API for use by tests
// in github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient and github.com/wekafs/csi-wekafs/pkg/wekafs.
//
// It exists so that package tests never dial a real Weka cluster (historically localhost:14000) -
// that made the suite slow (multi-second connection-refused retries) and impossible to run without
// a live cluster. This package must not import apiclient itself: apiclient's own tests are in
// package apiclient (internal tests), so importing a package that imports apiclient back would be
// an import cycle. Wire shapes are therefore redeclared locally from apiclient's source, not
// imported.
package apiclienttest

import (
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// config carries the tunables set via Option. Defaults are chosen so a freshly created Server
// behaves like a modern, fully-licensed Weka cluster: recent version, admin-equivalent role, no KMS.
type config struct {
	clusterVersion string
	userRole       string
	rotating       bool
	dataServices   bool
}

// Option configures a Server at construction time.
type Option func(*config)

// WithClusterVersion sets the Weka release string the fake cluster reports (e.g. "5.1.0"). The
// default is recent enough to satisfy every compatibility gate in apiclient/compatibility.go.
func WithClusterVersion(v string) Option {
	return func(c *config) { c.clusterVersion = v }
}

// WithUserRole sets the role returned by users/whoami (e.g. "ClusterAdmin", "CSI"). The default
// passes ApiClient.HasCSIPermissions().
func WithUserRole(role string) Option {
	return func(c *config) { c.userRole = role }
}

// WithRotatingEndpoints makes the nodes/processes listing return a different set of management
// endpoints on every call, driven by an internal counter. This is what
// ApiClient.UpdateApiEndpoints needs in order to genuinely replace its endpoint map between calls -
// exercising that replacement under -race is the point of the concurrency race tests.
func WithRotatingEndpoints() Option {
	return func(c *config) { c.rotating = true }
}

// WithDataServicesProcess adds a process carrying the DATASERV role to the nodes/processes listing,
// as a cluster with a data services container deployed reports. Note the containers listing is
// deliberately left unchanged: a real cluster reports such a container as an ordinary "backend", and
// the process role is the only place the distinction shows up.
func WithDataServicesProcess() Option {
	return func(c *config) { c.dataServices = true }
}

// Server is a fake Weka REST API. It is stateful: filesystems, NFS client groups, NFS permissions
// and quotas created through its handlers are held in memory and visible to later requests, so CRUD
// tests (create, read back by uid/name, delete) work the same way they would against a real cluster.
//
// All mutable state is guarded by mu, since Go tests may run in parallel against the same server
// instance (the apiclient package tests share one *ApiClient, and therefore one Server, for the
// whole test binary run).
type Server struct {
	httpServer *httptest.Server
	cfg        config

	mu              sync.Mutex
	filesystems     map[string]*filesystemRecord // by uid
	fsNameIndex     map[string]string            // name -> uid, used only to seed a permission's fs
	clientGroups    map[string]*clientGroupRecord
	nfsPermissions  map[string]*nfsPermissionRecord
	interfaceGroups []interfaceGroupRecord
	quotas          map[string]*quotaRecord

	nodeCallCount atomic.Int32
	mgmtPort      int
}

// New starts a fake Weka API server and registers t.Cleanup to close it once the test finishes.
// Use this from within a test function.
func New(t *testing.T, opts ...Option) *Server {
	t.Helper()
	s := newServer(opts...)
	t.Cleanup(s.Close)
	return s
}

// NewStandalone starts a fake Weka API server without registering any cleanup. It exists for
// callers that have no *testing.T to hand - namely TestMain, which runs once for the whole package
// and manages the server's lifetime itself. The caller must call Close.
func NewStandalone(opts ...Option) *Server {
	return newServer(opts...)
}

func newServer(opts ...Option) *Server {
	cfg := config{
		clusterVersion: "5.1.0",
		userRole:       "ClusterAdmin",
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	s := &Server{
		cfg:            cfg,
		filesystems:    make(map[string]*filesystemRecord),
		fsNameIndex:    make(map[string]string),
		clientGroups:   make(map[string]*clientGroupRecord),
		nfsPermissions: make(map[string]*nfsPermissionRecord),
		quotas:         make(map[string]*quotaRecord),
	}

	s.httpServer = httptest.NewServer(s.routes())
	if _, portStr, err := net.SplitHostPort(strings.TrimPrefix(s.httpServer.URL, "http://")); err == nil {
		if port, err := strconv.Atoi(portStr); err == nil {
			s.mgmtPort = port
		}
	}

	s.seedDefaults()

	return s
}

// Addr returns the "host:port" this server listens on, suitable for apiclient.Credentials.Endpoints.
func (s *Server) Addr() string {
	return strings.TrimPrefix(s.httpServer.URL, "http://")
}

// URL returns the fake server's base URL, e.g. "http://127.0.0.1:54321".
func (s *Server) URL() string {
	return s.httpServer.URL
}

// Close shuts down the underlying httptest server. Safe to call multiple times.
func (s *Server) Close() {
	s.httpServer.Close()
}

// seedDefaults populates the fake cluster with the fixtures the apiclient and wekafs package tests
// expect to already exist: filesystems "default" and "snapvolFilesystem" (referenced by name in
// several tests), an NFS interface group with an IP (GetNfsMountIp needs one to succeed), a second
// named interface group "Data" (TestGetNfsInterfaceGroup looks one up by that name), and a stray NFS
// permission on "snapvolFilesystem" (TestFindNfsPermissionsByFilesystemName expects to find and
// delete one).
func (s *Server) seedDefaults() {
	s.AddFilesystem("default", 10<<40, 5<<40)
	s.AddFilesystem("snapvolFilesystem", 10<<40, 5<<40)

	s.mu.Lock()
	s.interfaceGroups = []interfaceGroupRecord{
		{
			Uid:        uuid.New().String(),
			Name:       "default",
			SubnetMask: "255.255.255.0",
			Gateway:    "192.168.100.1",
			Ips:        []string{"192.168.100.10"},
			Type:       "NFS",
			Status:     "OK",
		},
		{
			Uid:        uuid.New().String(),
			Name:       "Data",
			SubnetMask: "255.255.255.0",
			Gateway:    "192.168.101.1",
			Ips:        []string{"192.168.101.10"},
			Type:       "NFS",
			Status:     "OK",
		},
	}
	s.mu.Unlock()

	permUid := uuid.New().String()
	s.mu.Lock()
	s.nfsPermissions[permUid] = &nfsPermissionRecord{
		Uid:               permUid,
		Filesystem:        "snapvolFilesystem",
		Group:             "default",
		Path:              "/",
		PermissionType:    "RW",
		SquashMode:        "none",
		SupportedVersions: []string{"V3", "V4"},
		AnonUid:           "65534",
		AnonGid:           "65534",
	}
	s.mu.Unlock()
}

// AddFilesystem seeds (or replaces) a filesystem visible to GetFileSystemByName/GetFileSystemByUid.
func (s *Server) AddFilesystem(name string, totalBytes, freeBytes int64) {
	fsUid := uuid.New().String()
	s.mu.Lock()
	defer s.mu.Unlock()
	// A filesystem re-added under a name that already exists replaces the old record but keeps
	// callers who cached the old uid working: the old record simply becomes unreachable by name.
	s.filesystems[fsUid] = &filesystemRecord{
		Uid:           fsUid,
		Name:          name,
		TotalCapacity: totalBytes,
		FreeTotal:     freeBytes,
	}
	s.fsNameIndex[name] = fsUid
}
