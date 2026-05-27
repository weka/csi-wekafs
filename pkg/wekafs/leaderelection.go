package wekafs

import (
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
)

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
