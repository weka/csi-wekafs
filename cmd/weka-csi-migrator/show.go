package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/wekafs/csi-wekafs/pkg/migrator/apply"
	"github.com/wekafs/csi-wekafs/pkg/migrator/archive"
)

func newShowCommand() *cobra.Command {
	var (
		passwordStdin   bool
		ignoreIntegrity bool
		kindFilter      string
		namespaceFilter string
		nameFilter      string
		outputDir       string
	)

	cmd := &cobra.Command{
		Use:   "show <archive>",
		Short: "Print the Kubernetes objects an archive contains",
		Long: `Print the objects an archive contains, as YAML.

Where 'list' summarises an archive, 'show' prints what would actually be applied, so the
manifests can be reviewed or validated before an import touches a cluster:

  weka-csi-migrator show cluster.wcsi | kubectl apply --dry-run=client -f -
  weka-csi-migrator show cluster.wcsi --kind PersistentVolume
  weka-csi-migrator show cluster.wcsi --output-dir ./review

Output is a multi-document YAML stream on stdout, in the order an import would apply it.
Note that credentials appear in full if the archive was exported with --include-secret-data.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reader, warnings, err := openArchive(args[0], passwordStdin, ignoreIntegrity)
			if err != nil {
				return err
			}
			logger := zerolog.Ctx(cmd.Context())
			for _, warning := range warnings {
				logger.Warn().Msg(warning)
			}

			selected := selectEntries(reader, kindFilter, namespaceFilter, nameFilter)
			if len(selected) == 0 {
				return fmt.Errorf("no objects in %s match the given filters", args[0])
			}

			if outputDir != "" {
				return extractEntries(cmd.Context(), reader, selected, outputDir)
			}
			return printEntries(cmd.OutOrStdout(), reader, selected)
		},
	}

	cmd.Flags().StringVar(&kindFilter, "kind", "", "only objects of this kind (e.g. PersistentVolume)")
	cmd.Flags().StringVarP(&namespaceFilter, "namespace", "n", "", "only objects in this namespace")
	cmd.Flags().StringVar(&nameFilter, "name", "", "only the object with this name")
	cmd.Flags().StringVar(&outputDir, "output-dir", "", "write one file per object into this directory instead of printing")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the archive password from stdin")
	addIgnoreIntegrityFlag(cmd, &ignoreIntegrity)

	return cmd
}

// selectEntries filters the manifest, preserving apply order so that the printed stream is
// directly usable with kubectl.
func selectEntries(r *archive.Reader, kind, namespace, name string) []archive.Entry {
	var selected []archive.Entry
	for _, orderedKind := range orderedKinds(r) {
		for _, entry := range r.Entries() {
			if entry.Kind != orderedKind {
				continue
			}
			if kind != "" && !strings.EqualFold(entry.Kind, kind) {
				continue
			}
			if namespace != "" && entry.Namespace != namespace {
				continue
			}
			if name != "" && entry.Name != name {
				continue
			}
			selected = append(selected, entry)
		}
	}
	return selected
}

// orderedKinds lists the kinds present in the archive, apply order first so the output can
// be piped straight into kubectl, with any unrecognised kinds appended rather than dropped.
func orderedKinds(r *archive.Reader) []string {
	seen := map[string]bool{}
	var kinds []string
	for _, kind := range apply.KnownKinds() {
		kinds = append(kinds, kind)
		seen[kind] = true
	}
	for _, entry := range r.Entries() {
		if !seen[entry.Kind] {
			kinds = append(kinds, entry.Kind)
			seen[entry.Kind] = true
		}
	}
	return kinds
}

func printEntries(out io.Writer, r *archive.Reader, entries []archive.Entry) error {
	for i, entry := range entries {
		body, ok := r.Body(entry.Path)
		if !ok {
			return fmt.Errorf("archive entry %q has no content", entry.Path)
		}
		if i > 0 {
			if _, err := fmt.Fprintln(out, "---"); err != nil {
				return err
			}
		}
		if _, err := out.Write(body); err != nil {
			return fmt.Errorf("writing %s: %w", entry.Path, err)
		}
	}
	return nil
}

// extractEntries writes each object to its own file, mirroring the archive's layout.
func extractEntries(ctx context.Context, r *archive.Reader, entries []archive.Entry, dir string) error {
	logger := zerolog.Ctx(ctx)
	for _, entry := range entries {
		body, ok := r.Body(entry.Path)
		if !ok {
			return fmt.Errorf("archive entry %q has no content", entry.Path)
		}
		// entry.Path is validated on open to contain no traversal, so joining is safe.
		target := filepath.Join(dir, entry.Path)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(target), err)
		}
		// 0600: an archive exported with --include-secret-data holds live credentials.
		if err := os.WriteFile(target, body, 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", target, err)
		}
		logger.Debug().Str("file", target).Msg("Extracted object")
	}
	logger.Info().Int("objects", len(entries)).Str("directory", dir).Msg("Extracted archive")
	return nil
}
