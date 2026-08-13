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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/wekafs/csi-wekafs/pkg/migrator/apply"
	"github.com/wekafs/csi-wekafs/pkg/migrator/archive"
	"github.com/wekafs/csi-wekafs/pkg/migrator/transform"
)

func newShowCommand() *cobra.Command {
	var (
		passwordStdin   bool
		ignoreIntegrity bool
		kindFilter      string
		namespaceFilter string
		nameFilter      string
		outputDir       string
		transformFile   string
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
  weka-csi-migrator show cluster.wcsi --transform-file dr.yaml

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

			chain, err := loadTransform(transformFile)
			if err != nil {
				return err
			}

			selected := selectEntries(reader, kindFilter, namespaceFilter, nameFilter)
			if len(selected) == 0 {
				return fmt.Errorf("no objects in %s match the given filters", args[0])
			}

			objects, err := renderEntries(reader, selected, chain)
			if err != nil {
				return err
			}
			// Report exactly what import would report, so a preview and the real run agree.
			for _, change := range chain.Changes() {
				logger.Debug().Msg(change.String())
			}
			if changes := len(chain.Changes()); changes > 0 {
				logger.Info().Int("changes", changes).Strs("rules", chain.Names()).Msg("Applied transform rules")
			}
			// Only meaningful over the whole archive: with a filter applied, a mapping that
			// matched nothing may simply target an object the filter excluded, and warning
			// about it would train the reader to ignore a warning that matters.
			if kindFilter == "" && namespaceFilter == "" && nameFilter == "" {
				for _, unused := range chain.UnusedMappings() {
					logger.Warn().Str("mapping", unused).Msg("Transform mapping matched no object in the archive")
				}
			}

			if outputDir != "" {
				return extractObjects(cmd.Context(), objects, outputDir)
			}
			return printObjects(cmd.OutOrStdout(), objects)
		},
	}

	cmd.Flags().StringVar(&kindFilter, "kind", "", "only objects of this kind (e.g. PersistentVolume)")
	cmd.Flags().StringVarP(&namespaceFilter, "namespace", "n", "", "only objects in this namespace")
	cmd.Flags().StringVar(&nameFilter, "name", "", "only the object with this name")
	cmd.Flags().StringVar(&outputDir, "output-dir", "", "write one file per object into this directory instead of printing")
	cmd.Flags().StringVar(&transformFile, "transform-file", "", "preview the objects as a transformed import would create them")
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

// renderedObject is one archive entry after any transform, kept alongside the path it should
// be written to so that extraction mirrors the archive layout.
type renderedObject struct {
	path string
	body []byte
}

// renderEntries decodes, transforms and re-encodes the selected entries.
//
// Transforming here rather than only at import is what makes `show --transform-file` an
// exact preview: both paths run the same chain over the same objects.
func renderEntries(r *archive.Reader, entries []archive.Entry, chain *transform.Chain) ([]renderedObject, error) {
	rendered := make([]renderedObject, 0, len(entries))
	for _, entry := range entries {
		body, ok := r.Body(entry.Path)
		if !ok {
			return nil, fmt.Errorf("archive entry %q has no content", entry.Path)
		}
		if chain == nil || chain.Len() == 0 {
			rendered = append(rendered, renderedObject{path: entry.Path, body: body})
			continue
		}

		var raw map[string]any
		if err := yaml.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("decoding %s: %w", entry.Path, err)
		}
		obj := &unstructured.Unstructured{Object: raw}
		if err := chain.Apply(obj); err != nil {
			return nil, fmt.Errorf("transforming %s: %w", entry.Path, err)
		}
		transformed, err := yaml.Marshal(obj.Object)
		if err != nil {
			return nil, fmt.Errorf("encoding %s: %w", entry.Path, err)
		}
		rendered = append(rendered, renderedObject{path: entry.Path, body: transformed})
	}
	return rendered, nil
}

func printObjects(out io.Writer, objects []renderedObject) error {
	for i, object := range objects {
		if i > 0 {
			if _, err := fmt.Fprintln(out, "---"); err != nil {
				return err
			}
		}
		if _, err := out.Write(object.body); err != nil {
			return fmt.Errorf("writing %s: %w", object.path, err)
		}
	}
	return nil
}

// extractObjects writes each object to its own file, mirroring the archive's layout.
func extractObjects(ctx context.Context, objects []renderedObject, dir string) error {
	logger := zerolog.Ctx(ctx)
	for _, object := range objects {
		// Paths are validated on open to contain no traversal, so joining is safe.
		target := filepath.Join(dir, object.path)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(target), err)
		}
		// 0600: an archive exported with --include-secret-data holds live credentials.
		if err := os.WriteFile(target, object.body, 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", target, err)
		}
		logger.Debug().Str("file", target).Msg("Extracted object")
	}
	logger.Info().Int("objects", len(objects)).Str("directory", dir).Msg("Extracted archive")
	return nil
}
