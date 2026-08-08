package apiclient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// pagedServer serves a fixed number of pages, echoing back which next_token it was asked for so a
// test can assert the client actually followed the chain.
func pagedServer(t *testing.T, pages [][]Quota, seen *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "quotas") {
			// login / token refresh - not part of what these tests count
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"access_token": "t", "refresh_token": "r", "expires_in": 3600},
			})
			return
		}
		token := r.URL.Query().Get("next_token")
		*seen = append(*seen, token)
		idx := 0
		if token != "" {
			_, _ = fmt.Sscanf(token, "page-%d", &idx)
		}
		body := map[string]any{"data": pages[idx]}
		if idx+1 < len(pages) {
			body["next_token"] = fmt.Sprintf("page-%d", idx+1)
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func testClient(t *testing.T, srv *httptest.Server) *ApiClient {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	c, err := NewApiClient(t.Context(), Credentials{
		Username: "u", Password: "p", Organization: "Root",
		Endpoints: []string{u.Host}, HttpScheme: "http",
	}, true, "test")
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	c.apiToken = "t"
	c.CompatibilityMap = &WekaCompatibilityMap{UrlQueryParams: true}
	return c
}

// Every page must reach the caller's object. The original implementation reassigned its local
// Response to an accumulator, so the caller kept only page 1.
func TestPaginationReturnsEveryPageToTheCaller(t *testing.T) {
	pages := [][]Quota{
		{{InodeId: 1}, {InodeId: 2}},
		{{InodeId: 3}, {InodeId: 4}},
		{{InodeId: 5}},
	}
	var seen []string
	srv := pagedServer(t, pages, &seen)
	defer srv.Close()

	got := &Quotas{}
	if err := testClient(t, srv).Get(t.Context(), "/quotas", url.Values{"filter": []string{"x"}}, got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(*got) != 5 {
		t.Fatalf("expected all 5 quotas across 3 pages, got %d: %+v", len(*got), *got)
	}
	for i, q := range *got {
		if q.InodeId != uint64(i+1) {
			t.Fatalf("page contents out of order at %d: %+v", i, *got)
		}
	}
	if len(seen) != 3 || seen[0] != "" || seen[1] != "page-1" || seen[2] != "page-2" {
		t.Fatalf("expected the client to follow the token chain, saw %v", seen)
	}
}

// FindQuotaByFilter passes a nil Query. Setting next_token on a nil url.Values panics, so the
// second page used to take the process down.
func TestPaginationWithNilQuery(t *testing.T) {
	pages := [][]Quota{{{InodeId: 1}}, {{InodeId: 2}}}
	var seen []string
	srv := pagedServer(t, pages, &seen)
	defer srv.Close()

	got := &Quotas{}
	if err := testClient(t, srv).Get(t.Context(), "/quotas", nil, got); err != nil {
		t.Fatalf("a nil query must be usable for a paginated response, got %v", err)
	}
	if len(*got) != 2 {
		t.Fatalf("expected 2 quotas, got %d", len(*got))
	}
}

// Pagination must not mutate the query the caller handed in.
func TestPaginationDoesNotMutateCallerQuery(t *testing.T) {
	pages := [][]Quota{{{InodeId: 1}}, {{InodeId: 2}}}
	var seen []string
	srv := pagedServer(t, pages, &seen)
	defer srv.Close()

	caller := url.Values{"filter": []string{"x"}}
	if err := testClient(t, srv).Get(t.Context(), "/quotas", caller, &Quotas{}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, polluted := caller["next_token"]; polluted {
		t.Fatalf("pagination leaked next_token into the caller's query: %v", caller)
	}
}

// A response type that does not implement ApiObjectResponse is fetched exactly once, as before.
func TestNonPaginatedResponseFetchesOnce(t *testing.T) {
	pages := [][]Quota{{{InodeId: 1}}, {{InodeId: 2}}}
	var seen []string
	srv := pagedServer(t, pages, &seen)
	defer srv.Close()

	got := &[]Quota{}
	if err := testClient(t, srv).Get(t.Context(), "/quotas", nil, got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("a non-paginated response must be fetched once, saw %d requests", len(seen))
	}
}
