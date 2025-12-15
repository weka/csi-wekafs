/*
A small utility that gates CSI sidecars on controller pods.

It waits for:
- a leader ready file to exist, and
- the CSI socket to accept connections,

then starts the real sidecar as a child process. While the sidecar is running, it
monitors the leader ready file; if leadership is lost, it terminates the sidecar
and returns to waiting.

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
	"os"
	"os/exec"
	"os/signal"
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
		"wait-for-leader: gating %s (leader file: %s, poll: %s, socket: %s, leader-loss debounce: %s)\n",
		binary,
		leaderFile,
		pollInterval.String(),
		socketPath,
		leaderLossDebounce.String(),
	)

	for {
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
