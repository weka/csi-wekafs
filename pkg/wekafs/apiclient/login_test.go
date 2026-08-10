package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// writeLoginJSON is local to this file on purpose. Borrowing the race test's writeJSON would couple
// these tests to a harness that a later change in the stack replaces wholesale, breaking this file
// from a commit that never touches it.
func writeLoginJSON(t *testing.T, w http.ResponseWriter, v interface{}) {
	t.Helper()
	inner, err := json.Marshal(v)
	if err != nil {
		t.Errorf("failed to marshal test fixture: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out, err := json.Marshal(struct {
		Data json.RawMessage `json:"data"`
	}{Data: inner})
	if err != nil {
		t.Errorf("failed to marshal test envelope: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// newLoginFailureTestServer stands up a fake Weka API where the login call itself always succeeds
// (publishing a token), but the whoami call that ensureSufficientPermissions makes right after
// returns a role that fails HasCSIPermissions, so Login fails during its post-auth setup - after
// the token is already visible to isLoggedIn().
//
// The whoami handler blocks the first request it receives until the test calls the returned
// release function, and it reports (via the returned channel) the instant it was entered. That lets
// a test start a second, independent Login call while the first is holding loginMu with its token
// already published but its setup not yet finished - the exact window fix 1 closes.
func newLoginFailureTestServer(t *testing.T) (hostPort string, whoamiEntered <-chan struct{}, release func()) {
	t.Helper()
	entered := make(chan struct{})
	var enteredOnce sync.Once
	gate := make(chan struct{})
	var gateOnce sync.Once

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/login", func(w http.ResponseWriter, r *http.Request) {
		writeLoginJSON(t, w, LoginResponse{
			AccessToken:  "access-token",
			TokenType:    "bearer",
			ExpiresIn:    3600,
			RefreshToken: "refresh-token",
		})
	})
	mux.HandleFunc("/api/v2/security/defaultTokensExpiry", func(w http.ResponseWriter, r *http.Request) {
		writeLoginJSON(t, w, TokenExpiryResponse{
			AccessTokenExpiry:  3600,
			RefreshTokenExpiry: 86400,
		})
	})
	mux.HandleFunc("/api/v2/users/whoami", func(w http.ResponseWriter, r *http.Request) {
		enteredOnce.Do(func() { close(entered) })
		<-gate
		// An empty role fails HasCSIPermissions, so ensureSufficientPermissions - and therefore
		// Login - returns an error after the token has already been published.
		writeLoginJSON(t, w, WhoamiResponse{OrgId: 1, Username: "nobody", Source: "local", Uid: uuid.New()})
	})
	mux.HandleFunc("/api/v2/cluster", func(w http.ResponseWriter, r *http.Request) {
		writeLoginJSON(t, w, ClusterInfoResponse{Name: "login-test-cluster", Release: "4.2.0", Guid: uuid.New()})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("failed to parse test server URL: %v", err)
	}
	return u.Host, entered, func() { gateOnce.Do(func() { close(gate) }) }
}

// TestLogin_FailedPostAuthSetupNotReportedAsSuccess reproduces the regression described in fix 1:
// Login publishes the token before ensureSufficientPermissions/fetchClusterInfo/UpdateApiEndpoints
// run, so isLoggedIn() alone goes true while the login is still in the middle of failing. Before the
// fix, both Login's opportunistic check before taking loginMu and its re-check after taking it used
// isLoggedIn(), so a second caller - whether it arrives while the first is still holding loginMu, or
// afterward once the first has already returned its error - was told the login succeeded, leaving it
// to operate with an all-false CompatibilityMap for the token's lifetime.
func TestLogin_FailedPostAuthSetupNotReportedAsSuccess(t *testing.T) {
	quietLogs(t)
	serverAddr, whoamiEntered, release := newLoginFailureTestServer(t)

	creds := Credentials{
		Username:     "admin",
		Password:     "admin",
		Organization: "Root",
		HttpScheme:   "http",
		Endpoints:    []string{serverAddr},
	}
	c, err := NewApiClient(context.Background(), creds, ApiClientOptions{Hostname: "login-test-client"})
	if err != nil {
		t.Fatalf("failed to create API client: %v", err)
	}

	err1Ch := make(chan error, 1)
	go func() {
		err1Ch <- c.Login(context.Background())
	}()

	select {
	case <-whoamiEntered:
		// The first Login has published its token and is now blocked inside the permission check,
		// still holding loginMu.
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the first login to reach the permission check")
	}

	err2Ch := make(chan error, 1)
	go func() {
		err2Ch <- c.Login(context.Background())
	}()

	release()

	err1 := <-err1Ch
	err2 := <-err2Ch

	if err1 == nil {
		t.Fatal("expected the first login's permission check failure to surface")
	}
	if err2 == nil {
		t.Fatal("a second Login must not report success for a login whose post-auth setup failed")
	}
}
