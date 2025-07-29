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

package wekafs

import (
	"github.com/rs/zerolog/log"
	"os"
)

type VolumeType string

// Die used to intentionally panic and exit, while updating termination log
func Die(exitMsg string) {
	_ = os.WriteFile("/dev/termination-log", []byte(exitMsg), 0644)
	panic(exitMsg)
}

type CsiPluginMode string

func GetCsiPluginMode(mode *string) CsiPluginMode {
	ret := CsiPluginMode(*mode)
	switch ret {
	case CsiModeNode,
		CsiModeController,
		CsiModeAll:
		return ret
	default:
		log.Fatal().Str("required_plugin_mode", string(ret)).Msg("Unsupported plugin mode")
		return ""
	}
}
