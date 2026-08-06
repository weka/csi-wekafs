package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// captureLogs points the log writer at a buffer for the duration of the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	original := stderr
	buf := &bytes.Buffer{}
	stderr = buf
	t.Cleanup(func() { stderr = original })
	return buf
}

func TestConfigureLoggingDefaultsToInfo(t *testing.T) {
	buf := captureLogs(t)

	logger, err := configureLogging(defaultLogLevel)
	if err != nil {
		t.Fatalf("configureLogging returned error: %v", err)
	}

	logger.Debug().Msg("debug-message")
	logger.Info().Msg("info-message")
	logger.Warn().Msg("warn-message")

	out := buf.String()
	if strings.Contains(out, "debug-message") {
		t.Error("debug output appeared at the default level")
	}
	for _, want := range []string{"info-message", "warn-message"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from output at the default level", want)
		}
	}
}

func TestConfigureLoggingRespectsLevel(t *testing.T) {
	for _, tc := range []struct {
		level     string
		wantDebug bool
		wantInfo  bool
		wantWarn  bool
	}{
		{"trace", true, true, true},
		{"debug", true, true, true},
		{"info", false, true, true},
		{"warn", false, false, true},
		{"error", false, false, false},
		{"off", false, false, false},
	} {
		t.Run(tc.level, func(t *testing.T) {
			buf := captureLogs(t)
			logger, err := configureLogging(tc.level)
			if err != nil {
				t.Fatalf("configureLogging(%q) returned error: %v", tc.level, err)
			}
			logger.Debug().Msg("dbg")
			logger.Info().Msg("nfo")
			logger.Warn().Msg("wrn")

			out := buf.String()
			if got := strings.Contains(out, "dbg"); got != tc.wantDebug {
				t.Errorf("debug visible = %v, want %v", got, tc.wantDebug)
			}
			if got := strings.Contains(out, "nfo"); got != tc.wantInfo {
				t.Errorf("info visible = %v, want %v", got, tc.wantInfo)
			}
			if got := strings.Contains(out, "wrn"); got != tc.wantWarn {
				t.Errorf("warn visible = %v, want %v", got, tc.wantWarn)
			}
		})
	}
}

func TestConfigureLoggingAcceptsMixedCaseAndSpace(t *testing.T) {
	captureLogs(t)
	for _, level := range []string{"INFO", " info ", "Info"} {
		if _, err := configureLogging(level); err != nil {
			t.Errorf("configureLogging(%q) returned error: %v", level, err)
		}
	}
}

func TestConfigureLoggingRejectsUnknownLevel(t *testing.T) {
	captureLogs(t)
	_, err := configureLogging("shouty")
	if err == nil {
		t.Fatal("an unknown level was accepted")
	}
	// The message must list the alternatives, since there is no other discovery path.
	for _, level := range []string{"trace", "info", "off"} {
		if !strings.Contains(err.Error(), level) {
			t.Errorf("error does not mention %q: %v", level, err)
		}
	}
}

// TestLoggingGoesToTheConfiguredWriter is the property the whole design hangs on: `export
// --output -` streams an archive to stdout and `list --json` streams JSON there, so a single
// stray log line on stdout would corrupt the output. Everything must land on the writer
// configureLogging was given, which in production is stderr.
func TestLoggingGoesToTheConfiguredWriter(t *testing.T) {
	buf := captureLogs(t)

	logger, err := configureLogging("trace")
	if err != nil {
		t.Fatalf("configureLogging returned error: %v", err)
	}
	logger.Trace().Msg("returned-logger-message")
	if !strings.Contains(buf.String(), "returned-logger-message") {
		t.Error("the returned logger did not write to the configured writer")
	}

	// The package-level logger must follow the same writer. A library reaching for the
	// global logger would otherwise fall back to zerolog's default, which is stdout.
	before := buf.Len()
	log.Info().Msg("global-logger-message")
	if !strings.Contains(buf.String()[before:], "global-logger-message") {
		t.Error("the global zerolog logger escaped the configured writer, risking stdout")
	}
}

// TestContextLoggerReachesLibraries covers the plumbing pkg/migrator relies on: it logs
// through zerolog.Ctx(ctx), which is silent unless the CLI attached a logger to the context.
func TestContextLoggerReachesLibraries(t *testing.T) {
	buf := captureLogs(t)

	logger, err := configureLogging("debug")
	if err != nil {
		t.Fatalf("configureLogging returned error: %v", err)
	}
	ctx := logger.WithContext(t.Context())

	zerolog.Ctx(ctx).Debug().Msg("library-message")
	if !strings.Contains(buf.String(), "library-message") {
		t.Error("a library logging through zerolog.Ctx produced no output")
	}
}
