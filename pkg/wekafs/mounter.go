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
	"context"

	"github.com/rs/zerolog/log"
)

func (driver *WekaFsDriver) NewMounter(ctx context.Context) AnyMounter {
	log.Info().Msg("Configuring Mounter")
	if driver.config.useNfs {
		log.Warn().Msg("Enforcing NFS transport due to configuration")
		return newNfsMounter(ctx, driver)
	}
	if driver.config.allowNfsFailback && !isWekaRunning(ctx) {
		if driver.config.isInDevMode() {
			log.Info().Msg("Not Enforcing NFS transport due to dev mode")
		} else {
			log.Warn().Msg("Weka Driver not found. Failing back to NFS transport")
			return newNfsMounter(ctx, driver)
		}
	}
	log.Info().Msg("Enforcing WekaFS transport")
	return newWekafsMounter(ctx, driver)
}
