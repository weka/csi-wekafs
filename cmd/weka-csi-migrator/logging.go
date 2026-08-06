package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// logLevels maps the names accepted by --log-level onto zerolog levels.
var logLevels = map[string]zerolog.Level{
	"trace": zerolog.TraceLevel,
	"debug": zerolog.DebugLevel,
	"info":  zerolog.InfoLevel,
	"warn":  zerolog.WarnLevel,
	"error": zerolog.ErrorLevel,
	"off":   zerolog.Disabled,
}

// defaultLogLevel is what the tool logs at when nothing is specified: enough to follow what
// an export or import is doing, without the per-object detail that debug adds.
const defaultLogLevel = "info"

// logLevelNames lists the accepted values, for flag help and error messages.
func logLevelNames() string {
	names := make([]string, 0, len(logLevels))
	for name := range logLevels {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return logLevels[names[i]] < logLevels[names[j]] })
	return strings.Join(names, ", ")
}

// configureLogging installs a human-readable console logger.
//
// Logs go to stderr, never stdout. `export --output -` writes the archive to stdout and
// `list --json` writes JSON there, so anything mixed into stdout would corrupt a pipeline.
func configureLogging(level string) (zerolog.Logger, error) {
	parsed, ok := logLevels[strings.ToLower(strings.TrimSpace(level))]
	if !ok {
		return log.Logger, fmt.Errorf("unknown log level %q: choose one of %s", level, logLevelNames())
	}

	writer := zerolog.ConsoleWriter{
		Out:        stderr,
		TimeFormat: time.RFC3339,
	}
	logger := zerolog.New(writer).Level(parsed).With().Timestamp().Logger()

	// Set the package-level logger too, so that any library reaching for the global one
	// writes to the same place rather than zerolog's default stdout.
	log.Logger = logger
	zerolog.SetGlobalLevel(parsed)

	return logger, nil
}
