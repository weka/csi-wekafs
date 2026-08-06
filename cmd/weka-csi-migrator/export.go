package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/wekafs/csi-wekafs/pkg/migrator/collect"
)

func newExportCommand(clients clusterConnector) *cobra.Command {
	var (
		output            string
		namespace         string
		driverName        string
		includeSecretData bool
		skipUnexportable  bool
		passwordStdin     bool
		encrypt           bool
		force             bool
	)

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export Weka CSI volume definitions to an archive",
		Long: `Export the Kubernetes objects that make up Weka CSI volumes.

Dynamically provisioned PersistentVolumes are converted to static form and their claims are
pinned to them, so that applying the archive to another cluster rebinds the original data
instead of provisioning new empty storage.

Credentials in Secrets are redacted unless --include-secret-data is given, which requires a
password so that the archive is encrypted.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Embedding credentials always requires encryption; --encrypt asks for it on its
			// own, for a redacted export that should still not be readable at rest.
			wantEncryption := includeSecretData || encrypt

			purpose := "to encrypt the archive"
			if includeSecretData {
				purpose = "to encrypt an archive containing secret data"
			}

			password, err := readPassword(passwordRequest{
				FromStdin: passwordStdin,
				Required:  wantEncryption,
				Purpose:   purpose,
				Prompt:    "Enter a password to encrypt the archive",
				// A typo would produce an archive nobody can open, and the mistake would
				// only surface when someone needs to restore from it.
				Confirm: true,
			})
			if err != nil {
				return err
			}
			// Enforced here rather than in the archive layer: it is a policy about how the
			// CLI may be used, not a property of the container format.
			if wantEncryption && password == "" {
				return fmt.Errorf("%s requires a password: set %s or pass --password-stdin",
					map[bool]string{true: "--include-secret-data", false: "--encrypt"}[includeSecretData],
					passwordEnvVar)
			}

			// Check the destination before doing any work. Collection is the slow part, and
			// discovering "that file already exists" only after it finishes wastes the whole
			// run for a problem that was knowable immediately.
			if err := checkOutputAvailable(output, force); err != nil {
				return err
			}

			logger := zerolog.Ctx(cmd.Context())

			client, contextName, err := clients.client()
			if err != nil {
				return err
			}
			logger.Info().
				Str("context", contextName).
				Str("driver", driverName).
				Str("namespace", namespaceOrAll(namespace)).
				Bool("include_secret_data", includeSecretData).
				Msg("Collecting Weka CSI volume definitions")

			collector := collect.New(client, collect.Options{
				DriverName:        driverName,
				Namespace:         namespace,
				IncludeSecretData: includeSecretData,
				SkipUnexportable:  skipUnexportable,
				Tool:              "weka-csi-migrator/" + version,
			})
			writer, err := collector.Collect(cmd.Context())
			if err != nil {
				return err
			}

			manifest := writer.Manifest()
			manifest.Source.Context = contextName
			writer.SetSource(manifest.Source)

			// Warnings are also persisted in the manifest, so `list` replays them later;
			// surfacing them now is what makes an interactive export self-explanatory.
			for _, warning := range writer.Warnings() {
				logger.Warn().Msg(warning)
			}

			if force && output != "-" {
				if _, err := os.Stat(output); err == nil {
					logger.Warn().Str("output", output).Msg("Overwriting an existing archive")
				}
			}

			out, err := openOutput(output, force)
			if err != nil {
				return err
			}
			defer out.cleanup()

			if err := writer.WriteTo(out, password); err != nil {
				return err
			}
			if err := out.commit(); err != nil {
				return err
			}

			logger.Info().
				Int("objects", writer.Len()).
				Int("volumes", len(manifest.Volumes)).
				Str("output", describeOutput(output)).
				Bool("encrypted", password != "").
				Msg("Export complete")
			return nil
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "archive to write, or - for stdout (required)")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "export only claims in this namespace (secrets and storage classes are still followed wherever they live)")
	cmd.Flags().StringVar(&driverName, "driver-name", collect.DefaultDriverName, "CSI driver name to export volumes for")
	cmd.Flags().BoolVar(&includeSecretData, "include-secret-data", false, "export credentials verbatim instead of redacting them; requires a password")
	cmd.Flags().BoolVar(&skipUnexportable, "skip-unexportable", false, "skip snapshot-backed volumes, which cannot be recreated against a different Weka cluster")
	cmd.Flags().BoolVar(&encrypt, "encrypt", false, "encrypt the archive with a password, even when credentials are redacted (implied by --include-secret-data)")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the archive password from stdin")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite the output file if it already exists")
	_ = cmd.MarkFlagRequired("output")

	return cmd
}

// checkOutputAvailable reports whether the destination can be written.
//
// An export must never silently overwrite an existing archive, which may be the only record
// of a cluster's volumes. The check races with anything else creating the file, but the
// window is negligible for a CLI and closing it would cost the atomic replace in openOutput.
func checkOutputAvailable(path string, force bool) error {
	if path == "-" || force {
		return nil
	}
	switch _, err := os.Stat(path); {
	case err == nil:
		return fmt.Errorf("%s already exists; pass --force to overwrite it, or choose another path", path)
	case !os.IsNotExist(err):
		return fmt.Errorf("checking %s: %w", path, err)
	default:
		return nil
	}
}

// output is a destination for an archive, written through a staging file so that the final
// path only ever holds a complete archive.
type output struct {
	io.Writer
	// commit moves the finished archive into place.
	commit func() error
	// cleanup discards the staging file. It is safe to call after commit.
	cleanup func()
}

// openOutput prepares the destination.
//
// A file destination is staged in a temporary file alongside the target and renamed into
// place only once the archive is complete. Writing directly would mean that a failure
// partway — a full disk, an interrupted run — leaves a truncated file that still looks like
// a backup, which is the worst possible outcome for a tool whose output may be the only
// record of a cluster's volumes.
func openOutput(path string, force bool) (*output, error) {
	if path == "-" {
		return &output{Writer: os.Stdout, commit: func() error { return nil }, cleanup: func() {}}, nil
	}

	if err := checkOutputAvailable(path, force); err != nil {
		return nil, err
	}

	// Stage in the target's own directory so that the rename stays on one filesystem and is
	// therefore atomic.
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	file, err := os.CreateTemp(dir, "."+base+".*.partial")
	if err != nil {
		return nil, fmt.Errorf("creating a staging file next to %s: %w", path, err)
	}
	staging := file.Name()

	return &output{
		Writer: file,
		commit: func() error {
			if err := file.Close(); err != nil {
				return fmt.Errorf("closing %s: %w", staging, err)
			}
			// CreateTemp makes the file 0600, which is what an archive holding credentials
			// wants; rename preserves it.
			if err := os.Rename(staging, path); err != nil {
				return fmt.Errorf("moving the finished archive into %s: %w", path, err)
			}
			return nil
		},
		cleanup: func() {
			_ = file.Close()
			// Removing an already-renamed staging path is a no-op error we can ignore.
			_ = os.Remove(staging)
		},
	}, nil
}

func describeOutput(path string) string {
	if path == "-" {
		return "stdout"
	}
	return path
}

// namespaceOrAll renders an empty namespace filter as something readable in a log line.
func namespaceOrAll(namespace string) string {
	if namespace == "" {
		return "<all>"
	}
	return namespace
}
