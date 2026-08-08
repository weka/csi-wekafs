package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Guards the fix: without DefaultContextLogger, log.Ctx on a bare context is disabled and the line
// vanishes. Startup paths and background goroutines both hand us such contexts.
func TestBareContextStillLogs(t *testing.T) {
	var buf bytes.Buffer
	prev, prevDefault := log.Logger, zerolog.DefaultContextLogger
	defer func() { log.Logger, zerolog.DefaultContextLogger = prev, prevDefault }()

	log.Logger = zerolog.New(&buf)

	zerolog.DefaultContextLogger = nil
	log.Ctx(context.Background()).Info().Msg("swallowed")
	if buf.Len() != 0 {
		t.Fatalf("expected the line to be dropped without the fallback, got %q", buf.String())
	}

	zerolog.DefaultContextLogger = &log.Logger
	log.Ctx(context.Background()).Info().Msg("survives")
	if !strings.Contains(buf.String(), "survives") {
		t.Fatalf("expected the fallback to emit the line, got %q", buf.String())
	}
}
