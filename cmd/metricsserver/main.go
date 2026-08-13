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

// Command metricsserver runs the Weka CSI metrics server on its own, without the CSI driver.
//
// It is the same code the plugin runs under -csimode=metricsserver; this binary exists so the
// metrics server can be deployed and scaled separately from the driver, with a flag set narrowed to
// what it actually reads. Everything specific to serving CSI - mounting, the gRPC surface, volume
// provisioning - is skipped by the mode itself, so this entry point only has to build a config,
// construct the driver and run it.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"

	"github.com/wekafs/csi-wekafs/pkg/bootstrap"
	"github.com/wekafs/csi-wekafs/pkg/wekafs"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// csiMode is fixed rather than a flag: this binary is the metrics server, and any other mode would
// need the driver machinery that main() below deliberately does not set up.
const csiMode = wekafs.CsiModeMetricsServer

var (
	driverName         = flag.String("drivername", "csi.weka.io", "name of the driver whose volumes to report on")
	nodeID             = flag.String("nodeid", "", "node id")
	showVersion        = flag.Bool("version", false, "Show version.")
	enableMetrics      = flag.Bool("enablemetrics", false, "Enable Prometheus metrics endpoint")
	metricsPort        = flag.String("metricsport", "9090", "HTTP port to expose metrics on")
	verbosity          = flag.Int("v", 1, "sets log verbosity level")
	tracingUrl         = flag.String("tracingurl", "", "OpenTelemetry / Jaeger endpoint")
	allowInsecureHttps = flag.Bool("allowinsecurehttps", false, "Allow insecure HTTPS connection without cert validation")
	usejsonlogging     = flag.Bool("usejsonlogging", false, "Use structured JSON logging rather than human-readable console log formatting")
	// Higher than the plugin's default: a quota map fetch pulls every quota on a filesystem in one
	// request, which on a large filesystem takes considerably longer than an ordinary call.
	wekaApiTimeoutSeconds = flag.Int("wekaapitimeoutseconds", 180, "Timeout for a single Weka API request in seconds")
	// On by default here: the metrics server polls the cluster continuously, so spreading requests
	// across management nodes keeps any single one from carrying the whole load.
	rotateApiEndpointOnEachRequest = flag.Bool("rotateapiendpointoneachrequest", true, "Send each Weka API request to a different management node rather than staying on one until it fails")

	wekaMetricsFetchIntervalSeconds          = flag.Int("wekametricsfetchintervalseconds", 60, "Interval in seconds to fetch metrics from Weka cluster")
	wekaMetricsFetchConcurrentRequests       = flag.Int("wekametricsfetchconcurrentrequests", 1, "Maximum concurrent requests to fetch metrics from Weka cluster")
	enableMetricsServerLeaderElection        = flag.Bool("enablemetricsserverleaderelection", false, "Enable leader election for metrics server")
	wekaMetricsQuotaUpdateConcurrentRequests = flag.Int("wekametricsquotaupdateconcurrentrequests", 5, "Maximum concurrent requests to update quotas for metrics server")
	wekaMetricsQuotaCacheValiditySeconds     = flag.Int("wekametricsquotacachevalidityseconds", 60, "Duration in seconds for which the quota map is considered valid")
	fetchQuotasInBatchMode                   = flag.Bool("fetchquotasinbatchmode", true, "Fetch all quotas of a filesystem in one request instead of one request per volume")

	// Set by the build process
	version = ""
)

func main() {
	flag.Parse()
	bootstrap.SetupLogging(*usejsonlogging, *verbosity)

	if *showVersion {
		fmt.Println(path.Base(os.Args[0]), version)
		return
	}
	log.Info().Str("csi_mode", string(csiMode)).Msg("Started Weka CSI metrics server")

	if *enableMetrics {
		// Only the API client's collectors are registered up front. The controller and node families
		// belong to modes this binary never runs, and exporting a permanently-zero copy of them would
		// be misleading; the metrics server's own collectors need the driver and are registered in
		// handle() once it exists.
		prometheus.MustRegister(apiclient.Collectors()...)
		bootstrap.ServeMetrics(*metricsPort)
	}

	ctx := context.Background()
	shutdownTracing := bootstrap.SetupTracing(ctx, version, *tracingUrl, csiMode)
	defer shutdownTracing()

	handle(ctx)
	os.Exit(0)
}

func handle(ctx context.Context) {
	// Only the settings the metrics server reads are set. The rest - mount options, provisioning
	// prefixes, per-operation concurrency - govern CSI request handling that this process never does,
	// and NewDriverConfig supplies its own defaults for the metrics knobs left at zero.
	config := wekafs.NewDriverConfig(wekafs.DriverConfigOptions{
		Version:                        version,
		AllowInsecureHttps:             *allowInsecureHttps,
		WekaApiTimeoutSeconds:          *wekaApiTimeoutSeconds,
		RotateApiEndpointOnEachRequest: *rotateApiEndpointOnEachRequest,
		TracingUrl:                     *tracingUrl,

		MetricsFetchIntervalSeconds:       *wekaMetricsFetchIntervalSeconds,
		MetricsFetchConcurrentRequests:    *wekaMetricsFetchConcurrentRequests,
		EnableMetricsServerLeaderElection: *enableMetricsServerLeaderElection,
		QuotaFetchConcurrentRequests:      *wekaMetricsQuotaUpdateConcurrentRequests,
		QuotaCacheValiditySeconds:         *wekaMetricsQuotaCacheValiditySeconds,
		UseQuotaMapsForMetrics:            *fetchQuotasInBatchMode,
	})

	// No endpoint and no volume limit: this process serves no gRPC and stages nothing.
	driver, err := wekafs.NewWekaFsDriver(*driverName, *nodeID, "", 0, version, "", csiMode, false, config)
	if err != nil {
		fmt.Printf("Failed to initialize metrics server: %s", err.Error())
		os.Exit(1)
	}
	config.SetDriver(driver)

	if *enableMetrics {
		if collectors := driver.MetricsServerCollectors(); len(collectors) > 0 {
			prometheus.MustRegister(collectors...)
		}
	}

	driver.Run(ctx)
}
