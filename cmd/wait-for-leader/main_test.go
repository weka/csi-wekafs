package main

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// The port is discovered from the command line the sidecar is already given, so that it is stated
// once rather than configured in two places that can drift apart.
func TestStandbyEndpointFromArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "the form the chart actually renders",
			args: []string{"/csi-provisioner", "--v=5", "--http-endpoint=:9091", "--leader-election=false"},
			want: ":9091",
		},
		{
			name: "separated by a space",
			args: []string{"/csi-attacher", "--http-endpoint", ":9095"},
			want: ":9095",
		},
		{
			name: "a trailing flag with no value is not mistaken for one",
			args: []string{"/csi-resizer", "--http-endpoint"},
			want: "",
		},
		// The health monitor is given no metrics endpoint and is not scraped, so it must end up
		// with no standby listener rather than a guessed one.
		{
			name: "the health monitor, which has none",
			args: []string{"/csi-external-health-monitor-controller", "--v=5", "--csi-address=$(ADDRESS)", "--timeout=300s"},
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := standbyEndpointFromArgs(tc.args); got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

// freePort returns an address nothing is listening on.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not find a free port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// A scrape of a standby replica has to succeed. Before this, nothing listened on the sidecar's port
// until leadership was acquired, so Prometheus held a refused target for the life of the replica.
func TestStandbyServerAnswersScrapesThenReleasesThePort(t *testing.T) {
	addr := freePort(t)
	standby := &standbyMetricsServer{addr: addr}

	standby.start()
	standby.start() // idempotent: the gating loop calls it on every pass

	var resp *http.Response
	var err error
	for i := 0; i < 50; i++ {
		resp, err = http.Get("http://" + addr + "/metrics")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("expected the standby listener to answer, got %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 so the target counts as up, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// The same endpoint serves the sidecars' health checks, so it answers there too.
	if hResp, hErr := http.Get("http://" + addr + "/healthz"); hErr != nil {
		t.Errorf("expected the standby listener to answer health checks, got %v", hErr)
	} else {
		if hResp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 on the health path, got %d", hResp.StatusCode)
		}
		_ = hResp.Body.Close()
	}

	// And it must be gone by the time stop() returns, or the sidecar it is gating cannot bind.
	standby.stop()
	l, bindErr := net.Listen("tcp", addr)
	if bindErr != nil {
		t.Fatalf("expected the port to be free once stop() returned, got %v", bindErr)
	}
	_ = l.Close()
}

// A sidecar with no metrics endpoint must not get a listener, and must not fail either.
func TestStandbyServerIsANoOpWithoutAnEndpoint(t *testing.T) {
	standby := &standbyMetricsServer{addr: ""}
	standby.start()
	if standby.cancel != nil {
		t.Error("expected no listener to be started when there is no endpoint to serve")
	}
	standby.stop() // must not panic or block
}

// The port can be taken when start() runs - the sidecar it just stopped may still be releasing it -
// and that must resolve itself rather than leaving the standby silent for the rest of its life.
func TestStandbyServerRetriesWhileThePortIsTaken(t *testing.T) {
	addr := freePort(t)
	blocker, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("could not occupy the port: %v", err)
	}

	standby := &standbyMetricsServer{addr: addr}
	standby.start()
	defer standby.stop()

	// Still blocked: nothing of ours can be answering yet.
	time.Sleep(50 * time.Millisecond)

	_ = blocker.Close()

	var lastErr error
	for i := 0; i < 100; i++ {
		resp, gErr := http.Get("http://" + addr + "/metrics")
		if gErr == nil {
			_ = resp.Body.Close()
			return
		}
		lastErr = gErr
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("expected the standby listener to take the port once it was free, last error: %v", fmt.Sprint(lastErr))
}
