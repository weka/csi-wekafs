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
	"path"
	"strconv"
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

}

var (
	csiMode                                  = wekafs.CsiModeMetricsServer
	driverName                               = flag.String("drivername", "csi.weka.io", "name of the driver")
	nodeID                                   = flag.String("nodeid", "", "node id")
	showVersion                              = flag.Bool("version", false, "Show version.")
	enableMetrics                            = flag.Bool("enablemetrics", false, "Enable Prometheus metrics endpoint")
	metricsPort                              = flag.String("metricsport", "9090", "HTTP port to expose metrics on")
	verbosity                                = flag.Int("v", 1, "sets log verbosity level")
	tracingUrl                               = flag.String("tracingurl", "", "OpenTelemetry / Jaeger endpoint")
	allowInsecureHttps                       = flag.Bool("allowinsecurehttps", false, "Allow insecure HTTPS connection without cert validation")
	usejsonlogging                           = flag.Bool("usejsonlogging", false, "Use structured JSON logging rather than human-readable console log formatting")
	grpcRequestTimeoutSeconds                = flag.Int("grpcrequesttimeoutseconds", 30, "Time out requests waiting in queue after X seconds")
	wekametricsfetchintervalseconds          = flag.Int("wekametricsfetchintervalseconds", 60, "Interval in seconds to fetch metrics from Weka cluster")
	wekametricsfetchconcurrentrequests       = flag.Int64("wekametricsfetchconcurrentrequests", 1, "Maximum concurrent requests to fetch metrics from Weka cluster")
	enableMetricsServerLeaderElection        = flag.Bool("enablemetricsserverleaderelection", false, "Enable leader election for metrics server")
	wekaMetricsQuotaUpdateConcurrentRequests = flag.Int("wekametricsquotaupdateconcurrentrequests", 5, "Maximum concurrent requests to update quotas for metrics server")
	wekaMetricsQuotaMapValidity              = flag.Int("wekametricsquotamapvalidityseconds", 60, "Duration for which the quota map is considered valid")
	wekaApiTimeout                           = flag.Int("wekaapitimeoutseconds", 120, "Timeout for Weka API requests in seconds")
	useBatchMode                             = flag.Bool("fetchquotasinbatchmode", false, "Use batch mode for metrics server, fetch all filesystem quotas in one go")
	// Set by the build process
	version = ""
)

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

func main() {
	flag.Parse()
	if !*usejsonlogging {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339Nano}).With().Caller().Logger()
	}
	zerolog.SetGlobalLevel(mapVerbosity(*verbosity))

	if *showVersion {
		baseName := path.Base(os.Args[0])
		fmt.Println(baseName, version)
		return
	}

	if enableMetrics != nil && *enableMetrics {
		go func() {
			http.Handle("/metrics", promhttp.Handler())
			if err := http.ListenAndServe(fmt.Sprintf(":%s", *metricsPort), nil); err != nil {
				log.Error().Str("metrics_port", *metricsPort).Err(err).Msg("Failed to start metrics service")
			}
			log.Debug().Str("metrics_port", *metricsPort).Msg("Started metrics service")
		}()
	}

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
		*allowInsecureHttps,
		false,
		wekafs.MutuallyExclusiveMountOptsStrings{},
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		*grpcRequestTimeoutSeconds,
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
		time.Duration(*wekametricsfetchintervalseconds)*time.Second,
		*wekametricsfetchconcurrentrequests,
		*enableMetricsServerLeaderElection,
		*wekaMetricsQuotaUpdateConcurrentRequests,
		time.Duration(*wekaMetricsQuotaMapValidity)*time.Second,
		time.Duration(*wekaApiTimeout)*time.Second,
		*useBatchMode,
	)
	driver, err := wekafs.NewWekaFsDriver(*driverName, *nodeID, "/dev/null", 0, version, csiMode, false, config)
	if err != nil {
		fmt.Printf("Failed to initialize driver: %s", err.Error())
		os.Exit(1)
	}
	config.SetDriver(driver)
	driver.Run(ctx)
}
