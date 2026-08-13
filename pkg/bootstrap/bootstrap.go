/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package bootstrap holds the process-level startup shared by the binaries in cmd/ - logging, the
// Prometheus scrape endpoint and OpenTelemetry tracing. None of it is specific to a CSI mode, so
// a second entry point (the standalone metrics server) reuses it rather than restating it.
package bootstrap

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	"github.com/wekafs/csi-wekafs/pkg/wekafs"
)

func init() {
	rand.Seed(time.Now().UnixNano())
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	zerolog.CallerMarshalFunc = func(pc uintptr, file string, line int) string {
		short := file
		for i := len(file) - 1; i > 0; i-- {
			if file[i] == '/' {
				short = file[i+1:]
				break
			}
		}
		file = short
		return file + ":" + strconv.Itoa(line)
	}
}

func mapVerbosity(verbosity int) zerolog.Level {
	verbMap := make(map[int]zerolog.Level)

	verbMap[0] = zerolog.Disabled
	verbMap[1] = zerolog.PanicLevel
	verbMap[2] = zerolog.FatalLevel
	verbMap[3] = zerolog.ErrorLevel
	verbMap[4] = zerolog.InfoLevel
	verbMap[5] = zerolog.DebugLevel
	verbMap[6] = zerolog.TraceLevel

	v := verbosity
	if v >= len(verbMap) {
		v = len(verbMap) - 1
	}
	return verbMap[v]
}

// SetupLogging configures the global zerolog logger from the -usejsonlogging and -v flags.
func SetupLogging(useJsonLogging bool, verbosity int) {
	if !useJsonLogging {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339Nano}).With().Caller().Logger()
	}
	zerolog.SetGlobalLevel(mapVerbosity(verbosity))
	// log.Ctx returns a *disabled* logger for a context that carries none, so anything written from
	// a startup path or a background goroutine - neither of which passes through a gRPC handler that
	// attaches one - is discarded without a trace. Fall back to the global logger instead, so a
	// missing context logger costs the request fields rather than the whole line.
	zerolog.DefaultContextLogger = &log.Logger
}

// ServeMetrics starts the Prometheus scrape endpoint in the background. Collectors are registered by
// the caller, which is the only place that knows which role this process serves.
func ServeMetrics(metricsPort string) {
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(fmt.Sprintf(":%s", metricsPort), nil); err != nil {
			log.Error().Str("metrics_port", metricsPort).Err(err).Msg("Failed to start metrics service")
		}
		log.Debug().Str("metrics_port", metricsPort).Msg("Started metrics service")
	}()
}

// SetupTracing installs the OpenTelemetry tracer provider and returns a function that flushes and
// shuts it down. Tracing is optional, so a provider that fails to build is logged and skipped; the
// returned function is always safe to call, letting callers defer it unconditionally.
func SetupTracing(ctx context.Context, version, tracingUrl string, csiMode wekafs.CsiPluginMode) func() {
	tp, err := wekafs.TracerProvider(version, tracingUrl, csiMode)
	if err != nil {
		log.Error().Err(err).Msg("Failed to set up OpenTelemetry tracerProvider")
		return func() {}
	}

	otel.SetTracerProvider(tp)
	log.Info().Str("tracing_url", tracingUrl).Msg("OpenTelemetry tracing initialized")

	// Interrupting the process cancels the context the flush below runs under, so a Ctrl-C during
	// shutdown gives up on the traces rather than hanging on an exporter that will never answer.
	ctx, cancel := context.WithCancel(ctx)
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		select {
		case <-c:
			cancel()
		case <-ctx.Done():
		}
	}()

	return func() {
		if err := tp.ForceFlush(ctx); err != nil {
			log.Error().Err(err).Msg("Failed to flush traces")
		} else {
			log.Info().Msg("Flushed traces successfully")
		}

		if err := tp.Shutdown(ctx); err != nil {
			log.Error().Err(err).Msg("Failed to shutdown tracing engine")
		} else {
			log.Info().Msg("Tracing engine shut down successfully")
		}

		signal.Stop(c)
		cancel()
	}
}
