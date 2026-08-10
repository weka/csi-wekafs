package apiclient

import (
	"context"
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

// Finding 1: a cluster that can't accept URL query params (old cluster, or an unparseable version
// string) silently drops next_token. Looping in that state re-sends the same request and gets
// page 1 back every time; the fix is to fetch exactly one page and warn, matching what this call
// did before pagination existed. Revert the SupportsUrlQueryParams check in Request and this test
// fails by asserting more than one request went out (in fact it would hang looping to 1000).
func TestPaginationWithoutUrlQuerySupportMakesExactlyOneRequest(t *testing.T) {
	pages := [][]Quota{
		{{InodeId: 1}, {InodeId: 2}},
		{{InodeId: 3}},
	}
	var seen []string
	srv := pagedServer(t, pages, &seen)
	defer srv.Close()

	c := testClient(t, srv)
	c.CompatibilityMap = &WekaCompatibilityMap{UrlQueryParams: false}

	got := &Quotas{}
	if err := c.Get(t.Context(), "/quotas", url.Values{"filter": []string{"x"}}, got); err != nil {
		t.Fatalf("Get must not fail just because the cluster can't accept a pagination token: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("expected exactly one request when the cluster cannot accept next_token, saw %d: %v", len(seen), seen)
	}
	if len(*got) != 2 {
		t.Fatalf("expected only page 1's data (truncated), got %d entries: %+v", len(*got), *got)
	}
}

// Finding 2: a backend that hands back the same token it was just given must not be followed
// forever. Revert the currentToken check in Request and this test fails because no error comes
// back (the loop would instead run until ApiMaxPagesPerRequest).
func TestPaginationStopsOnNonAdvancingToken(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "quotas") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"access_token": "t", "refresh_token": "r", "expires_in": 3600},
			})
			return
		}
		seen = append(seen, r.URL.Query().Get("next_token"))
		// Always hands back the same token, regardless of what was sent - a backend contract
		// violation that must not be looped on indefinitely.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":       []Quota{{InodeId: 1}},
			"next_token": "stuck",
		})
	}))
	defer srv.Close()

	got := &Quotas{}
	err := testClient(t, srv).Get(t.Context(), "/quotas", nil, got)
	if err == nil {
		t.Fatalf("expected an error when the backend never advances the pagination token")
	}
	if len(seen) == 0 || len(seen) > 5 {
		t.Fatalf("expected only a handful of requests before giving up on a stuck token, saw %d: %v", len(seen), seen)
	}
}

// Finding 3: a page whose data fails to unmarshal must surface as an error from Request, not be
// folded in as an empty page. Revert the unmarshalErr check in request() and this test fails
// because Get returns nil with a truncated (empty) result instead of an error.
func TestPaginationDataUnmarshalErrorIsReturned(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "quotas") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"access_token": "t", "refresh_token": "r", "expires_in": 3600},
			})
			return
		}
		seen = append(seen, r.URL.Query().Get("next_token"))
		// "data" is a string here, never a valid []Quota - unmarshalling it always fails.
		_ = json.NewEncoder(w).Encode(map[string]any{"data": "not-a-quota-list"})
	}))
	defer srv.Close()

	got := &Quotas{}
	err := testClient(t, srv).Get(t.Context(), "/quotas", nil, got)
	if err == nil {
		t.Fatalf("expected an error when a page's data fails to unmarshal, got nil (result: %+v)", *got)
	}
	if len(seen) != 1 {
		t.Fatalf("expected exactly one request before the unmarshal failure was surfaced, saw %d", len(seen))
	}
}

// Finding 4: do() cannot abort an in-flight request (it uses http.NewRequest, not
// NewRequestWithContext), so the pagination loop itself must notice a cancelled ctx and stop
// before issuing the next page's request. Revert the ctx.Err() check in Request's loop and this
// test fails because a 3rd request goes out and no error is returned.
func TestPaginationStopsOnCancelledContext(t *testing.T) {
	pages := [][]Quota{
		{{InodeId: 1}},
		{{InodeId: 2}},
		{{InodeId: 3}},
	}
	var seen []string
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := pagedServer(t, pages, &seen)
	defer srv.Close()
	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r)
		if strings.Contains(r.URL.Path, "quotas") {
			// Cancel once the first page has been served. The HTTP transport never observes
			// this (that's finding 4's premise), so only the loop's own ctx.Err() check can
			// stop the second request from going out.
			cancel()
		}
	})

	got := &Quotas{}
	err := testClient(t, srv).Get(ctx, "/quotas", nil, got)
	if err == nil {
		t.Fatalf("expected the cancelled context's error to be returned")
	}
	if len(seen) != 1 {
		t.Fatalf("expected pagination to stop right after the context was cancelled, saw %d requests: %v", len(seen), seen)
	}
}

// Finding 5: CombinePartialResponse appends, so a caller-supplied accumulator that already has
// entries must be reset before the first page is fetched, or the pre-existing entries survive
// alongside the freshly fetched ones. Revert the reflect.Zero reset in Request and this test fails
// because the accumulator ends up with 3 pre-existing + 2 fetched entries instead of just the 2.
func TestPaginationReplacesPrepopulatedAccumulator(t *testing.T) {
	pages := [][]Quota{
		{{InodeId: 1}},
		{{InodeId: 2}},
	}
	var seen []string
	srv := pagedServer(t, pages, &seen)
	defer srv.Close()

	got := &Quotas{{InodeId: 997}, {InodeId: 998}, {InodeId: 999}}
	if err := testClient(t, srv).Get(t.Context(), "/quotas", nil, got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(*got) != 2 {
		t.Fatalf("expected the pre-populated accumulator to be replaced, not appended to; got %d entries: %+v", len(*got), *got)
	}
}

// Finding 8: Request decides whether to paginate purely from the response type, so a mutating
// method (POST/PUT/DELETE) whose response type happens to implement ApiObjectResponse must still
// take the single-request path - otherwise the same payload would be re-sent once per page. Revert
// the Method == http.MethodGet check and this test fails because a 2nd POST goes out carrying
// next_token.
func TestNonGetMethodWithPaginatedResponseTypeMakesOneRequest(t *testing.T) {
	pages := [][]Quota{
		{{InodeId: 1}},
		{{InodeId: 2}},
	}
	var seen []string
	srv := pagedServer(t, pages, &seen)
	defer srv.Close()

	got := &Quotas{}
	payload := []byte("{}")
	if err := testClient(t, srv).Post(t.Context(), "/quotas", &payload, nil, got); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("expected exactly one request for a non-GET method, saw %d: %v", len(seen), seen)
	}
	if seen[0] != "" {
		t.Fatalf("expected no next_token to be sent for a non-GET method, saw next_token=%q", seen[0])
	}
}

// Finding 6: a nil *Quotas still satisfies ApiObjectResponse (the interface value carries a type
// even though the pointer is nil), so it takes the paginated path. Without an explicit nil check,
// CombinePartialResponse's `*q = append(*q, ...)` dereferences that nil pointer and panics. Revert
// the IsNil check in Request and this test fails via the recover() catching a panic instead of
// seeing a plain error.
func TestPaginationNilTypedPointerErrorsWithoutPanic(t *testing.T) {
	var seen []string
	srv := pagedServer(t, [][]Quota{{{InodeId: 1}}}, &seen)
	defer srv.Close()

	var got *Quotas // typed nil, still satisfies ApiObjectResponse via its pointer-receiver method
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("a nil typed pointer must return an error, not panic: %v", r)
			}
		}()
		err := testClient(t, srv).Get(t.Context(), "/quotas", nil, got)
		if err == nil {
			t.Fatalf("expected an error for a nil *Quotas response, got nil")
		}
	}()
	if len(seen) != 0 {
		t.Fatalf("expected the nil check to reject the call before any request went out, saw %d", len(seen))
	}
}

// A 200 whose body carries no "data" key at all is not a decode failure: every DELETE passes
// &ApiResponse{} and gets exactly that shape back, and a paginated fetch may legitimately return
// an empty page. Unmarshalling a nil data field fails with "unexpected end of JSON input", so
// without the len(rawResponse.Data) == 0 guard this turns every successful delete into a
// permanent ApiNonTransientError - and the CSI sidecar retries a delete that already succeeded.
// Remove that guard and this test fails.
func TestBodylessSuccessIsNotAnUnmarshalFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "quotas") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"access_token": "t", "refresh_token": "r", "expires_in": 3600},
			})
			return
		}
		// Exactly what a Weka DELETE returns: a success envelope with no data key.
		_, _ = w.Write([]byte(`{"status":"OK"}`))
	}))
	defer srv.Close()

	resp := &ApiResponse{}
	if err := testClient(t, srv).Delete(t.Context(), "/quotas/x", nil, nil, resp); err != nil {
		t.Fatalf("a body-less 200 must not be reported as an unmarshal failure: %v", err)
	}
}

// The same guard must not resurrect the truncation bug for paginated fetches: an empty page ends
// the fetch cleanly rather than erroring, and the caller gets the pages that did arrive.
func TestPaginationEmptyPageEndsFetchWithoutError(t *testing.T) {
	var seen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "quotas") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"access_token": "t", "refresh_token": "r", "expires_in": 3600},
			})
			return
		}
		seen++
		if seen == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":       []Quota{{InodeId: 1}},
				"next_token": "page-2",
			})
			return
		}
		// Second page carries no data key and no token - a legitimate empty tail.
		_, _ = w.Write([]byte(`{"status":"OK"}`))
	}))
	defer srv.Close()

	got := &Quotas{}
	if err := testClient(t, srv).Get(t.Context(), "/quotas", nil, got); err != nil {
		t.Fatalf("an empty final page must end the fetch, not fail it: %v", err)
	}
	if len(*got) != 1 {
		t.Fatalf("expected the 1 quota from page 1, got %d", len(*got))
	}
	if seen != 2 {
		t.Fatalf("expected 2 requests (page 1 + empty tail), saw %d", seen)
	}
}
