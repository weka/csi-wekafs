/*
A small utility that gates CSI sidecars on controller pods.

It waits for:
- a leader ready file to exist, and
- the CSI socket to accept connections,

then starts the real sidecar as a child process. While the sidecar is running, it
monitors the leader ready file; if leadership is lost, it terminates the sidecar
and returns to waiting.

While the sidecar is not running, it answers HTTP on the sidecar's own metrics port
so that scrapes of a standby replica succeed with no metrics, rather than being
refused. See standbyMetricsServer.

Usage: wait-for-leader <command> [args...]

Environment variables:

	LEADER_READY_FILE: Path to the leader ready file (default: /leader-state/leader_ready)
	WAIT_POLL_INTERVAL: Poll interval in seconds (default: 1)
	CSI_SOCKET_PATH: Path to the CSI socket (default: /csi/csi.sock)
	LEADER_LOSS_DEBOUNCE: Seconds to wait before treating leader file disappearance as leadership loss (default: 3)
	STOP_GRACE_PERIOD: Seconds to wait after SIGTERM before SIGKILLing the child (default: 10)
*/
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	defaultLeaderReadyFile        = "/leader-state/leader_ready"
	defaultPollIntervalSecs       = 1
	defaultSocketPath             = "/csi/csi.sock"
	defaultLeaderLossDebounceSecs = 3
	defaultStopGracePeriodSecs    = 10
)

var errLeadershipLost = errors.New("leadership lost")

// Set by the build process, as for the other cmd binaries: one
// -X main.version stamps every main package in a single go build.
var version = ""

func parseEnvInt(name string, defaultValue int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return defaultValue
	}
	return parsed
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func waitForLeaderFile(ctx context.Context, leaderFile string, pollInterval time.Duration) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		if fileExists(leaderFile) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitForSocket(ctx context.Context, socketPath, leaderFile string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		// In case leadership lost, return to waiting for the leader file.
		if !fileExists(leaderFile) {
			return errLeadershipLost
		}
		conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// standbyEndpointFromArgs finds the address the gated sidecar would serve its metrics on, so the
// port is discovered from the command line already being passed rather than configured twice. The
// four scraped sidecars are given "--http-endpoint=:<port>"; the health monitor is given none, and
// is not scraped, so it correctly gets no standby listener.
func standbyEndpointFromArgs(args []string) string {
	const flagName = "--http-endpoint"
	for i, arg := range args {
		if strings.HasPrefix(arg, flagName+"=") {
			return strings.TrimPrefix(arg, flagName+"=")
		}
		if arg == flagName && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// standbyMetricsServer answers on the gated sidecar's metrics port for as long as that sidecar is
// not running - which, on a standby replica, is its whole life.
//
// The controller PodMonitor scrapes every replica's sidecar ports, but the sidecars only start once
// leadership is acquired. Nothing listens on the standby, so Prometheus holds a refused target per
// sidecar per standby replica, indefinitely, and any alert written on "up == 0" fires on a cluster
// that is behaving exactly as designed. An empty 200 is the honest answer: the target is reachable
// and has no metrics, because the process that would produce them is not meant to run here.
//
// The port is held by exactly one of the two at a time - stop() is called before the child starts,
// and start() again only after it has been reaped.
type standbyMetricsServer struct {
	addr   string
	cancel context.CancelFunc
	done   chan struct{}
}

// start is idempotent, and never blocks: gating the sidecar is this binary's real job, so a port
// that cannot be bound is reported and retried rather than allowed to hold up leadership.
func (s *standbyMetricsServer) start() {
	if s.addr == "" || s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})

	go func() {
		defer close(s.done)

		mux := http.NewServeMux()
		// Every path, not just /metrics: the same endpoint serves the sidecars' own health checks,
		// and a standby answering 200 there is as true as it is for the metrics path.
		mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
		})

		for {
			listener, err := net.Listen("tcp", s.addr)
			if err != nil {
				// Usually the sidecar we just stopped still holding the port. Retry, so this does
				// not become a permanent hole for the rest of the standby period.
				fmt.Fprintf(os.Stderr, "wait-for-leader: standby listener on %s not up yet: %v\n", s.addr, err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
					continue
				}
			}

			server := &http.Server{Handler: mux}
			go func() {
				<-ctx.Done()
				_ = server.Close()
			}()
			fmt.Printf("wait-for-leader: standby, answering scrapes on %s\n", s.addr)
			_ = server.Serve(listener)
			return
		}
	}()
}

// stop releases the port and waits until it is actually free, so the child can bind it.
func (s *standbyMetricsServer) stop() {
	if s.cancel == nil {
		return
	}
	s.cancel()
	<-s.done
	s.cancel = nil
	s.done = nil
}

func startChild(binary string, args []string) (*exec.Cmd, <-chan error, error) {
	cmd := exec.Command(binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	return cmd, done, nil
}

func terminateChild(cmd *exec.Cmd, childDone <-chan error, grace time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-childDone:
	case <-time.After(grace):
		_ = cmd.Process.Kill()
		<-childDone
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <command> [args...]\n", os.Args[0])
		os.Exit(1)
	}

	// Checked before os.Args[1] is taken as the command to gate. No flag package here: everything
	// after the binary name belongs to the child, so only this exact single argument is intercepted.
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Println(path.Base(os.Args[0]), version)
		return
	}

	termCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	leaderFile := strings.TrimSpace(os.Getenv("LEADER_READY_FILE"))
	if leaderFile == "" {
		leaderFile = defaultLeaderReadyFile
	}

	pollInterval := time.Duration(parseEnvInt("WAIT_POLL_INTERVAL", defaultPollIntervalSecs)) * time.Second

	socketPath := strings.TrimSpace(os.Getenv("CSI_SOCKET_PATH"))
	if socketPath == "" {
		socketPath = defaultSocketPath
	}

	leaderLossDebounce := time.Duration(parseEnvInt("LEADER_LOSS_DEBOUNCE", defaultLeaderLossDebounceSecs)) * time.Second
	stopGrace := time.Duration(parseEnvInt("STOP_GRACE_PERIOD", defaultStopGracePeriodSecs)) * time.Second

	binary := os.Args[1]
	args := os.Args[2:]

	fmt.Printf(
		"wait-for-leader: version %s, gating %s (leader file: %s, poll: %s, socket: %s, leader-loss debounce: %s)\n",
		version,
		binary,
		leaderFile,
		pollInterval.String(),
		socketPath,
		leaderLossDebounce.String(),
	)

	standby := &standbyMetricsServer{addr: standbyEndpointFromArgs(args)}

	for {
		// Held for exactly as long as the sidecar is not running, including every return trip
		// through this loop after a lost leadership. Idempotent, so the continue below is safe.
		standby.start()

		fmt.Printf("wait-for-leader: waiting for leader file: %s\n", leaderFile)
		if err := waitForLeaderFile(termCtx, leaderFile, pollInterval); err != nil {
			os.Exit(0)
		}

		fmt.Printf("wait-for-leader: waiting for socket: %s\n", socketPath)
		if err := waitForSocket(termCtx, socketPath, leaderFile); err != nil {
			if errors.Is(err, errLeadershipLost) {
				continue
			}
			os.Exit(0)
		}

		// Released before the child is started, and only then, so the sidecar can bind its own
		// port. stop() waits for the listener to actually close rather than just asking it to.
		standby.stop()

		fmt.Printf("wait-for-leader: leader is ready, starting: %s\n", binary)
		cmd, childDone, err := startChild(binary, args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wait-for-leader: failed to start %s: %v\n", binary, err)
			os.Exit(1)
		}

		missingSince := time.Time{}
		ticker := time.NewTicker(pollInterval)

	monitorLoop:
		for {
			select {
			case <-termCtx.Done():
				terminateChild(cmd, childDone, stopGrace)
				os.Exit(0)
			case err := <-childDone:
				if err == nil {
					os.Exit(0)
				}
				if exitErr, ok := err.(*exec.ExitError); ok {
					os.Exit(exitErr.ExitCode())
				}
				fmt.Fprintf(os.Stderr, "wait-for-leader: child exited with error: %v\n", err)
				os.Exit(1)
			case <-ticker.C:
				if fileExists(leaderFile) {
					missingSince = time.Time{}
					continue
				}

				if missingSince.IsZero() {
					missingSince = time.Now()
					continue
				}

				if time.Since(missingSince) < leaderLossDebounce {
					continue
				}

				fmt.Printf("wait-for-leader: leader file missing, stopping: %s\n", binary)
				terminateChild(cmd, childDone, stopGrace)
				break monitorLoop
			}
		}
		ticker.Stop()
	}
}
