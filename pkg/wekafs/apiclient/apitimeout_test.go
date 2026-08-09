package apiclient

import (
	"testing"
	"time"
)

// An unset timeout must fall back to the built-in default, not to http.Client's zero value - which
// means no timeout at all. A caller that forgot to set it would otherwise hang on an unresponsive
// cluster forever instead of failing, which is worse than the timeout being merely wrong.
func TestApiTimeoutOrDefault(t *testing.T) {
	defaultTimeout := time.Duration(ApiHttpTimeOutSeconds) * time.Second

	for name, tc := range map[string]struct {
		given, want time.Duration
	}{
		"configured":         {180 * time.Second, 180 * time.Second},
		"unset":              {0, defaultTimeout},
		"negative":           {-1 * time.Second, defaultTimeout},
		"shorter than usual": {5 * time.Second, 5 * time.Second},
	} {
		if got := apiTimeoutOrDefault(tc.given); got != tc.want {
			t.Errorf("%s: apiTimeoutOrDefault(%v) = %v, want %v", name, tc.given, got, tc.want)
		}
	}
}

// The timeout must reach the HTTP client, which is the only thing that enforces it.
func TestApiClientAppliesConfiguredTimeout(t *testing.T) {
	for name, tc := range map[string]struct {
		opts ApiClientOptions
		want time.Duration
	}{
		"configured": {ApiClientOptions{ApiTimeout: 180 * time.Second}, 180 * time.Second},
		"unset":      {ApiClientOptions{}, time.Duration(ApiHttpTimeOutSeconds) * time.Second},
	} {
		client, err := NewApiClient(t.Context(), Credentials{Endpoints: []string{"127.0.0.1:14000"}}, tc.opts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if client.client.Timeout != tc.want {
			t.Errorf("%s: http client timeout = %v, want %v", name, client.client.Timeout, tc.want)
		}
	}
}
