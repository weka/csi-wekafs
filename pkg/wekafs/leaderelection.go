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
	"fmt"
	"os"
	"sync"

	"github.com/rs/zerolog/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// runWithLeaderElection runs the controller with leader election enabled.
// Only the leader starts the gRPC server; standby pods wait for leadership.
func (driver *WekaFsDriver) runWithLeaderElection(ctx context.Context, termContext context.Context, s *nonBlockingGRPCServer) {
	log.Info().Msg("Running controller with leader election")

	runCtx, cancelRun := context.WithCancel(ctx)
	var shutdownOnce sync.Once

	// Keep volume conditions fresh in the background so ListVolumes stays a pure cache read.
	// Registered as an ordinary runnable, which controller-runtime gates on leadership, so exactly
	// one replica probes the fleet and the loop stops when leadership is lost.
	if driver.cs != nil && driver.cs.healthReconciler != nil {
		if err := driver.manager.Add(manager.RunnableFunc(driver.cs.healthReconciler.Start)); err != nil {
			log.Error().Err(err).Msg("Failed to register volume health reconciler, conditions will not be reported")
		}
	}

	// Register the metrics server's health checks and leadership-gated Runnable, same as the health
	// reconciler above. Nil-guarded since it is only constructed when explicitly enabled and the
	// driver is running in a mode with Kubernetes access (see NewWekaFsDriver). Must happen before
	// driver.manager.Start below - controller-runtime rejects Add calls once the manager is started.
	if driver.metricsServer != nil {
		if err := driver.metricsServer.AddToManager(); err != nil {
			log.Error().Err(err).Msg("Failed to register metrics server, metrics will not be available")
		}
	}

	// Add runnable that starts gRPC server when we become leader
	err := driver.manager.Add(manager.RunnableFunc(func(ctx context.Context) error {
		// This only runs when we are the leader
		log.Info().Msg("Became leader - starting gRPC server")

		s.Start(driver.endpoint, driver.ids, driver.cs, driver.ns)

		// Mark as leader for health checks
		driver.isLeader.Store(true)

		// Signal to sidecars that we are the leader
		if err := createLeaderReadyFile(); err != nil {
			log.Error().Err(err).Msg("Failed to create leader ready file")
		}

		// Wait for context cancellation (leadership lost or shutdown)
		<-ctx.Done()

		log.Info().Msg("Leadership lost or shutdown - stopping gRPC server")

		// Mark as not leader for health checks
		driver.isLeader.Store(false)

		// Remove leader ready file before stopping gRPC
		if err := removeLeaderReadyFile(); err != nil {
			log.Error().Err(err).Msg("Failed to remove leader ready file")
		}

		s.Stop() // GracefulStop blocks until in-flight RPCs complete
		// Lease is held until this function returns (LeaderElectionReleaseOnCancel: true)

		return nil
	}))
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to add gRPC runnable to manager")
	}

	// Handle termination signal
	go func() {
		<-termContext.Done()
		log.Info().Msg("Received termination signal")

		shutdownOnce.Do(func() {
			if (driver.csiMode == CsiModeNode || driver.csiMode == CsiModeAll) &&
				driver.config.manageNodeTopologyLabels {
				driver.CleanupNodeLabels(ctx)
			}
			cancelRun()
		})
	}()

	// Start manager (blocks until shutdown)
	log.Info().Msg("Starting manager - waiting for leadership")
	if err := driver.manager.Start(runCtx); err != nil {
		log.Error().Err(err).Msg("Manager exited with error")
	}

	s.Wait()
	log.Info().Msg("Shutdown complete")
}

// runWithoutLeaderElection runs the driver without leader election (for node-only mode)
func (driver *WekaFsDriver) runWithoutLeaderElection(ctx context.Context, termContext context.Context, s *nonBlockingGRPCServer) {
	log.Info().Msg("Running without leader election (node-only mode)")

	runCtx, cancelRun := context.WithCancel(ctx)

	// If a manager was initialized (for K8s client access in node mode), start it so
	// its cache syncs and GetClient() returns a working client.
	if driver.manager != nil {
		go func() {
			if err := driver.manager.Start(runCtx); err != nil {
				log.Error().Err(err).Msg("Kubernetes manager exited with error")
			}
		}()
	}

	go func() {
		<-termContext.Done()
		if (driver.csiMode == CsiModeNode || driver.csiMode == CsiModeAll) &&
			driver.config.manageNodeTopologyLabels {
			log.Info().Msg("Cleanup of node labels...")
			driver.CleanupNodeLabels(ctx)
		}
		cancelRun()
		s.Stop()
		log.Info().Msg("Server stopped")
		os.Exit(1)
	}()

	s.Start(driver.endpoint, driver.ids, driver.cs, driver.ns)
	s.Wait()
}

// createLeaderReadyFile creates a file to signal sidecars that this pod is the leader
func createLeaderReadyFile() error {
	if err := os.MkdirAll(LeaderStateDir, 0o755); err != nil {
		return fmt.Errorf("failed to create leader state directory: %w", err)
	}

	if err := os.WriteFile(LeaderReadyFile, []byte{}, 0o644); err != nil {
		return fmt.Errorf("failed to create leader ready file: %w", err)
	}

	log.Info().Str("file", LeaderReadyFile).Msg("Created leader ready file")
	return nil
}

// removeLeaderReadyFile removes the leader ready file when losing leadership
func removeLeaderReadyFile() error {
	if err := os.Remove(LeaderReadyFile); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to remove leader ready file: %w", err)
	}
	log.Info().Str("file", LeaderReadyFile).Msg("Removed leader ready file (pod is not leader)")
	return nil
}
