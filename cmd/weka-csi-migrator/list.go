package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/wekafs/csi-wekafs/pkg/migrator/apply"
	"github.com/wekafs/csi-wekafs/pkg/migrator/archive"
)

func newListCommand() *cobra.Command {
	var (
		passwordStdin   bool
		asJSON          bool
		ignoreIntegrity bool
	)

	cmd := &cobra.Command{
		Use:   "list <archive>",
		Short: "Show what an archive contains",
		Long: `Show what an archive contains without touching any cluster.

Use this to review an export before restoring it, and to see which volumes are
snapshot-backed and therefore restorable only to a cluster attached to the same Weka
cluster.`,
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
			logger.Debug().
				Str("archive", args[0]).
				Bool("encrypted", reader.Header.Encrypted).
				Int("objects", len(reader.Entries())).
				Msg("Archive verified")
			if asJSON {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(reader.Manifest)
			}
			return printManifest(cmd.OutOrStdout(), reader)
		},
	}

	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the archive password from stdin")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the manifest as JSON")
	addIgnoreIntegrityFlag(cmd, &ignoreIntegrity)

	return cmd
}

func printManifest(out io.Writer, r *archive.Reader) error {
	m := r.Manifest

	fmt.Fprintf(out, "Created:    %s by %s\n", m.CreatedAt.Format("2006-01-02 15:04:05 MST"), m.Tool)
	fmt.Fprintf(out, "Driver:     %s\n", m.DriverName)
	fmt.Fprintf(out, "Encrypted:  %t\n", r.Header.Encrypted)
	if m.Namespace != "" {
		fmt.Fprintf(out, "Namespace:  %s\n", m.Namespace)
	}
	if source := strings.TrimSpace(m.Source.Context + " " + m.Source.ServerVersion); source != "" {
		fmt.Fprintf(out, "Source:     %s\n", source)
	}
	if m.Source.KubeSystemUID != "" {
		fmt.Fprintf(out, "Cluster ID: %s\n", m.Source.KubeSystemUID)
	}

	secrets := "redacted"
	if m.SecretsIncluded {
		secrets = "included"
	}
	fmt.Fprintf(out, "Secrets:    %s\n", secrets)

	if len(m.Volumes) > 0 {
		fmt.Fprintf(out, "\nVolumes (%d):\n", len(m.Volumes))
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  PV\tCLAIM\tFILESYSTEM\tBACKING\tSIZE\tPORTABLE")
		for _, v := range m.Volumes {
			claim := "-"
			if v.PVCName != "" {
				claim = v.PVCNamespace + "/" + v.PVCName
			}
			portable := "yes"
			if !v.PortableAcrossWekaClusters {
				portable = "same weka only"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", v.PVName, claim, v.FilesystemName, v.Backing, v.Capacity, portable)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	counts := map[string]int{}
	for _, entry := range m.Entries {
		counts[entry.Kind]++
	}
	// Taking the kind order from apply keeps this summary in step with what an import can
	// actually recreate, instead of drifting when a new kind is supported.
	fmt.Fprintf(out, "\nObjects (%d):\n", len(m.Entries))
	for _, kind := range apply.KnownKinds() {
		if counts[kind] > 0 {
			fmt.Fprintf(out, "  %-24s %d\n", kind, counts[kind])
			delete(counts, kind)
		}
	}
	// Anything the manifest holds that this build cannot apply is still worth showing.
	for _, kind := range slices.Sorted(maps.Keys(counts)) {
		fmt.Fprintf(out, "  %-24s %d (not supported by this version)\n", kind, counts[kind])
	}

	if len(m.RedactedSecretKeys) > 0 {
		fmt.Fprintf(out, "\nRedacted secret keys:\n")
		for path, keys := range m.RedactedSecretKeys {
			fmt.Fprintf(out, "  %s: %s\n", strings.TrimPrefix(path, "objects/secrets/"), strings.Join(keys, ", "))
		}
	}

	if len(m.Warnings) > 0 {
		fmt.Fprintf(out, "\nWarnings (%d):\n", len(m.Warnings))
		for _, warning := range m.Warnings {
			fmt.Fprintf(out, "  - %s\n", warning)
		}
	}
	return nil
}
