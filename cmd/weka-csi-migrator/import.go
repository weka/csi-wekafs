package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/wekafs/csi-wekafs/pkg/migrator/apply"
	"github.com/wekafs/csi-wekafs/pkg/migrator/archive"
	"github.com/wekafs/csi-wekafs/pkg/migrator/transform"
)

func newImportCommand(clients clusterConnector) *cobra.Command {
	var (
		input                string
		dryRun               bool
		skipExisting         bool
		allowRedactedSecrets bool
		passwordStdin        bool
		ignoreIntegrity      bool
	)

	cmd := &cobra.Command{
		Use:   "import <archive>",
		Short: "Recreate exported Weka CSI volume definitions on a cluster",
		Long: `Recreate the objects in an archive on the cluster the current kubeconfig points at.

Objects are applied in dependency order: Secrets and StorageClasses first, then
PersistentVolumes, then their claims. Nothing is overwritten; an object that already exists
aborts the import unless --skip-existing is given.

The archive is verified in full before anything is written.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input = args[0]

			logger := zerolog.Ctx(cmd.Context())

			reader, warnings, err := openArchive(input, passwordStdin, ignoreIntegrity)
			if err != nil {
				return err
			}
			logger.Info().
				Str("archive", input).
				Bool("encrypted", reader.Header.Encrypted).
				Int("objects", len(reader.Entries())).
				Str("exported_by", reader.Manifest.Tool).
				Time("exported_at", reader.Manifest.CreatedAt).
				Msg("Archive verified")

			for _, warning := range warnings {
				logger.Warn().Msg(warning)
			}
			for _, warning := range reader.Manifest.Warnings {
				logger.Warn().Str("origin", "export").Msg(warning)
			}

			client, contextName, err := clients.client()
			if err != nil {
				return err
			}
			logger.Info().
				Str("context", contextName).
				Bool("dry_run", dryRun).
				Msg("Applying objects")

			applier := apply.New(client, apply.Options{
				DryRun:               dryRun,
				SkipExisting:         skipExisting,
				AllowRedactedSecrets: allowRedactedSecrets,
				// v1 has no rules, so this is the identity transform. Later phases populate
				// the chain from a mapping file without touching the import path.
				Transform: transform.NewChain(),
			})
			results, err := applier.Apply(cmd.Context(), reader)

			out := cmd.OutOrStdout()
			for _, result := range results {
				fmt.Fprintf(out, "%-12s %-22s %s\n", result.Action, result.Kind, describeResult(result))
			}
			if err != nil {
				return err
			}

			message := "Import complete"
			if dryRun {
				message = "Dry run complete, nothing was written"
			}
			logger.Info().Int("objects", len(results)).Msg(message)
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be created without writing anything")
	cmd.Flags().BoolVar(&skipExisting, "skip-existing", false, "leave objects that already exist untouched instead of failing")
	cmd.Flags().BoolVar(&allowRedactedSecrets, "allow-redacted-secrets", false, "import an archive whose credentials were redacted; the secrets must be fixed by hand afterwards")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the archive password from stdin")

	addIgnoreIntegrityFlag(cmd, &ignoreIntegrity)

	return cmd
}

// openArchive resolves the password, opens the file and verifies it.
//
// The file is read into memory rather than streamed so that an encrypted archive can be
// retried after prompting. Archives hold Kubernetes metadata, and archive.Open buffers the
// payload anyway, so nothing is lost by it.
func openArchive(path string, passwordStdin, ignoreIntegrity bool) (*archive.Reader, []string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("opening %s: %w", path, err)
	}

	password, err := readPassword(passwordRequest{FromStdin: passwordStdin, Purpose: "to decrypt the archive"})
	if err != nil {
		return nil, nil, err
	}

	open := func(password string) (*archive.Reader, []string, error) {
		return archive.Open(bytes.NewReader(raw), archive.OpenOptions{
			Password:              password,
			IgnoreIntegrityErrors: ignoreIntegrity,
		})
	}

	reader, warnings, err := open(password)
	// Whether a password is needed is only knowable once the header has been read, so ask
	// for one here rather than demanding it up front for every archive.
	if errors.Is(err, archive.ErrPasswordRequired) {
		password, err = readPassword(passwordRequest{
			Required: true,
			Purpose:  "to decrypt the archive",
			Prompt:   fmt.Sprintf("Enter the password for %s", filepath.Base(path)),
		})
		if err != nil {
			return nil, nil, err
		}
		reader, warnings, err = open(password)
	}
	if err != nil {
		return nil, nil, err
	}

	if ignoreIntegrity && len(warnings) > 0 {
		warnings = append(warnings, "integrity checking was disabled: the contents of this archive are not trustworthy")
	}
	return reader, warnings, nil
}

func describeResult(r apply.Result) string {
	if r.Namespace == "" {
		return r.Name
	}
	return r.Namespace + "/" + r.Name
}
