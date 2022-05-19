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
	"flag"
	"fmt"
	"github.com/golang/glog"
	"github.com/wekafs/csi-wekafs/pkg/wekafs"
	"math/rand"
	"os"
	"path"
	"time"
)

func init() {
	_ = flag.Set("logtostderr", "true")
	rand.Seed(time.Now().UnixNano())
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
	csimodetext    = flag.String("csimode", "all", "Mode of CSI plugin, either \"controller\", \"node\", \"all\" (default)")
	selinuxSupport = flag.Bool("selinux-support", false, "Enable support for SELinux")
	// Set by the build process
	version = ""
)

func main() {
	flag.Parse()
	csiMode = wekafs.GetCsiPluginMode(csimodetext)
	if *showVersion {
		baseName := path.Base(os.Args[0])
		fmt.Println(baseName, version)
		return
	}
	if csiMode != wekafs.CsiModeAll && csiMode != wekafs.CsiModeController && csiMode != wekafs.CsiModeNode {
		wekafs.Die("Invalid mode specified for CSI driver")
	}
	glog.Infof("Running in mode: %s, SELinux support: %s", csiMode, func() string {
		if *selinuxSupport {
			return "ON"
		}
		return "OFF"
	}())

	handle()
	os.Exit(0)
}

func handle() {
	driver, err := wekafs.NewWekaFsDriver(*driverName, *nodeID, *endpoint, *maxVolumesPerNode, version, *debugPath, *dynamicSubPath, csiMode, *selinuxSupport)
	if err != nil {
		fmt.Printf("Failed to initialize driver: %s", err.Error())
		os.Exit(1)
	}
	driver.Run()
}
