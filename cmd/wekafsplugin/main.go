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
	csiMode    = wekafs.CsiPluginMode("all")
	endpoint   = flag.String("endpoint", "unix://tmp/csi.sock", "CSI endpoint")
	driverName = flag.String("drivername", "csi.weka.io", "name of the driver")
	debugPath  = flag.String("debugpath", "",
		"Debug path to use instead of actually mounting weka, can be local fs or wekafs,"+
			" virtual FS will be created in this path instead of actual mounting")
	nodeID            = flag.String("nodeid", "", "node id")
	maxVolumesPerNode = flag.Int64("maxvolumespernode", 0, "limit of volumes per node")
	showVersion       = flag.Bool("version", false, "Show version.")
	dynamicSubPath    = flag.String("dynamic-path", "csi-volumes",
		"Store dynamically provisioned volumes in subdirectory rather than in root directory of th filesystem")
	csimodetext                   = flag.String("csimode", "all", "Mode of CSI plugin, either \"controller\", \"node\", \"all\" (default)")
	selinuxSupport                = flag.Bool("selinux-support", false, "Enable support for SELinux")
	newVolumePrefix               = flag.String("newvolumeprefix", "csivol-", "Prefix for Weka volumes and snapshots that represent a CSI volume")
	newSnapshotPrefix             = flag.String("newsnapshotprefix", "csisnp-", "Prefix for Weka snapshots that represent a CSI snapshot")
	seedSnapshotPrefix            = flag.String("seedsnapshotprefix", "csisnp-seed-", "Prefix for empty (seed) snapshot to create on newly provisioned filesystem")
	allowAutoFsExpansion          = flag.Bool("allowautofsexpansion", false, "Allow expansion of filesystems used as CSI volumes")
	allowAutoFsCreation           = flag.Bool("allowautofscreation", false, "Allow provisioning of CSI volumes as new Weka filesystems")
	allowSnapshotsOfLegacyVolumes = flag.Bool("allowsnapshotsoflegacyvolumes", false, "Allow provisioning of CSI volumes or snapshots from legacy volumes")
	allowAutoSeedSnapshotCreation = flag.Bool("allowautoseedsnapshotcreation", false, "Allow automatic creation of empty snapshot on new filesystem")
	suppressSnapshotsCapability   = flag.Bool("suppresssnapshotcapability", false, "Do not expose CREATE_DELETE_SNAPSHOT, for testing purposes only")
	suppressVolumeCloneCapability = flag.Bool("suppressrvolumeclonecapability", false, "Do not expose CLONE_VOLUME, for testing purposes only")
	enableMetrics                 = flag.Bool("enablemetrics", false, "Enable Prometheus metrics endpoint")
	metricsPort                   = flag.String("metricsport", "9090", "HTTP port to expose metrics on")
	verbosity                     = flag.Int("v", 1, "sets log verbosity level")
	tracingUrl                    = flag.String("tracingurl", "", "OpenTelemetry / Jaeger endpoint")
	allowInsecureHttps            = flag.Bool("allowinsecurehttps", false, "Allow insecure HTTPS connection without cert validation")
	alwaysAllowSnapshotVolumes    = flag.Bool("alwaysallowsnapshotvolumes", false, "Allow snapshot-based volumes even when Weka cluster doesn't support capacity enforcement")
	usejsonlogging                = flag.Bool("usejsonlogging", false, "Use structured JSON logging rather than human-readable console log formatting")

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
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).With().Caller().Logger()
	}
	zerolog.SetGlobalLevel(mapVerbosity(*verbosity))

	csiMode = wekafs.GetCsiPluginMode(csimodetext)
	if *showVersion {
		baseName := path.Base(os.Args[0])
		fmt.Println(baseName, version)
		return
	}
	if csiMode != wekafs.CsiModeAll && csiMode != wekafs.CsiModeController && csiMode != wekafs.CsiModeNode {
		log.Panic().Str("requestedCsiMode", string(csiMode)).Msg("Invalid mode specified for CSI driver")
	}
	log.Info().Str("csi_mode", string(csiMode)).Bool("selinux_mode", *selinuxSupport).Msg("Started CSI driver")

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
	tp, err = wekafs.TracerProvider(version, url)
	if err != nil {
		log.Error().Err(err).Msg("Failed to set up OpenTelemetry tracerProvider")
	} else {
		otel.SetTracerProvider(tp)
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

	handle()
	os.Exit(0)
}

func handle() {
	config := wekafs.NewDriverConfig(*dynamicSubPath,
		*newVolumePrefix, *newSnapshotPrefix,
		*seedSnapshotPrefix, *debugPath,
		*allowAutoFsCreation, *allowAutoFsExpansion,
		*allowAutoSeedSnapshotCreation, *allowSnapshotsOfLegacyVolumes,
		*suppressSnapshotsCapability, *suppressVolumeCloneCapability,
		*allowInsecureHttps, *alwaysAllowSnapshotVolumes)
	driver, err := wekafs.NewWekaFsDriver(
		*driverName, *nodeID, *endpoint, *maxVolumesPerNode, version, *debugPath, csiMode, *selinuxSupport, config)
	if err != nil {
		fmt.Printf("Failed to initialize driver: %s", err.Error())
		os.Exit(1)
	}
	driver.Run()
}
