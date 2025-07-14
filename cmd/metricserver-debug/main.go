package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
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
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339Nano}).With().Caller().Logger()
}

var (
	csiMode                                  = wekafs.CsiModeMetricsServer
	driverName                               = "csi.weka.io"
	nodeID, _                                = os.Hostname()
	enableMetrics                            = true
	metricsPort                              = 9096
	allowInsecureHttps                       = true
	tracingUrl                               = flag.String("tracingurl", "", "OpenTelemetry / Jaeger endpoint")
	wekametricsfetchintervalseconds          = 300
	enableMetricsServerLeaderElection        = false
	wekaMetricsQuotaMapGetConcurrentRequests = flag.Int("quotamap-concurrent-requests", 1, "Maximum concurrent requests to fetch list of all quotas from Weka cluster")
	wekaMetricsQuotaMapValidity              = 0
	wekametricsfetchconcurrentrequests       = flag.Int64("api-concurrent-requests", 1, "Maximum concurrent requests to fetch metrics from Weka cluster")
	wekaApiTimeout                           = flag.Int("api-timeout-seconds", 120, "Timeout for Weka API requests in seconds")
	useBatchMode                             = flag.Bool("batch-mode", false, "Use batch mode for metrics server, fetch all filesystem quotas in one go")
	// Set by the build process
	version = ""
)

func main() {

	flag.Parse()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(fmt.Sprintf(":%d", metricsPort), nil); err != nil {
			log.Error().Int("metrics_port", metricsPort).Err(err).Msg("Failed to start metrics service")
		}
	}()

	ctx := context.Background()
	var tp *sdktrace.TracerProvider
	var err error
	var url string
	if *tracingUrl != "" {
		url = *tracingUrl

	} else {
		url = ""
	}
	tp, err = wekafs.TracerProvider(version, url, csiMode)

	if err != nil {
		log.Error().Err(err).Msg("Failed to set up OpenTelemetry tracerProvider")
	} else {
		otel.SetTracerProvider(tp)
		log.Info().Str("tracing_url", url).Msg("OpenTelemetry tracing initialized")
		ctx, cancel := context.WithCancel(ctx)
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		defer func() {
			signal.Stop(c)
			cancel()
		}()
		go func() {
			select {
			case <-c:
				cancel()
			case <-ctx.Done():
			}
		}()

		defer func() {
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

		}()
	}

	handle(ctx)
	os.Exit(0)
}

func handle(ctx context.Context) {

	config := wekafs.NewDriverConfig(
		"",
		"",
		"",
		"",
		false,
		false,
		false,
		false,
		false,
		true,
		false,
		wekafs.MutuallyExclusiveMountOptsStrings{},
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		30,
		false,
		false,
		false,
		"",
		"",
		"",
		version,
		false,
		false,
		false,
		*tracingUrl,
		false,
		time.Duration(wekametricsfetchintervalseconds)*time.Second,
		*wekametricsfetchconcurrentrequests,
		enableMetricsServerLeaderElection,
		*wekaMetricsQuotaMapGetConcurrentRequests,
		time.Duration(wekaMetricsQuotaMapValidity)*time.Second,
		time.Duration(*wekaApiTimeout)*time.Second,
		*useBatchMode,
	)
	driver, err := wekafs.NewWekaFsDriver(driverName, nodeID, "/dev/null", 0, version, csiMode, false, config)
	if err != nil {
		fmt.Printf("Failed to initialize driver: %s", err.Error())
		os.Exit(1)
	}
	config.SetDriver(driver)
	driver.Ms = wekafs.NewMetricsServer(driver)
	termContext, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() {
		<-termContext.Done()
		driver.Ms.Stop(ctx)
		os.Exit(1)
	}()

	if *useBatchMode {

		driver.Ms.StartDebugQuotaMaps(ctx)
	} else {
		driver.Ms.StartDebugSingleQuotas(ctx)
	}
	driver.Ms.Wait()

}
