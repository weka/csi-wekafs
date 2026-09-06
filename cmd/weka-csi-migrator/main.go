// Command weka-csi-migrator exports and restores the Kubernetes objects that make up Weka
// CSI volumes.
//
// It moves Kubernetes metadata only. The data itself stays on the Weka cluster, which is
// what makes it possible to rebuild a lost Kubernetes cluster without touching storage.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

// stderr carries logs and interactive prompts. It is a variable so tests can capture them,
// and it is never stdout: see configureLogging.
var stderr io.Writer = os.Stderr

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	var kubeconfig, kubeContext, logLevel string

	cmd := &cobra.Command{
		Use:   "weka-csi-migrator",
		Short: "Export and restore Weka CSI volume definitions",
		Long: strings.TrimSpace(`
Export and restore the Kubernetes objects that make up Weka CSI volumes.

The tool reads and writes Kubernetes objects only; it never contacts the Weka cluster and
never touches volume data. An export captures PersistentVolumes, their claims, the
StorageClasses and Secrets they reference, converts dynamically provisioned volumes into
static form, and writes them to a single archive.

Passwords are read from ` + passwordEnvVar + `, from --password-stdin, or by prompting when
the terminal is interactive. They are never taken from command-line arguments.`),
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			logger, err := configureLogging(logLevel)
			if err != nil {
				return err
			}
			// Attach the logger to the command context so that pkg/migrator can log through
			// zerolog.Ctx(ctx), matching the driver's request-scoped logging convention.
			cmd.SetContext(logger.WithContext(cmd.Context()))
			return nil
		},
	}

	cmd.PersistentFlags().StringVar(&kubeconfig, "kubeconfig", "", "path to a kubeconfig file (defaults to $KUBECONFIG, then ~/.kube/config)")
	cmd.PersistentFlags().StringVar(&kubeContext, "context", "", "kubeconfig context to use (defaults to the current context)")
	cmd.PersistentFlags().StringVar(&logLevel, "log-level", defaultLogLevel, "log verbosity, written to stderr ("+logLevelNames()+")")

	clients := &clientFactory{kubeconfig: &kubeconfig, context: &kubeContext}
	cmd.AddCommand(newExportCommand(clients), newImportCommand(clients), newListCommand(), newShowCommand())
	return cmd
}

// clusterConnector yields a Kubernetes client and the name of the context it came from.
// Commands depend on this rather than on clientFactory so that tests can drive them against
// a fake cluster.
type clusterConnector interface {
	client() (kubernetes.Interface, string, error)
}

// clientFactory builds a Kubernetes client lazily, so that `list`, which needs no cluster,
// never requires a working kubeconfig.
type clientFactory struct {
	kubeconfig *string
	context    *string
}

func (f *clientFactory) client() (kubernetes.Interface, string, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if *f.kubeconfig != "" {
		rules.ExplicitPath = *f.kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if *f.context != "" {
		overrides.CurrentContext = *f.context
	}

	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("loading kubeconfig: %w", err)
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, "", fmt.Errorf("building Kubernetes client: %w", err)
	}

	contextName := *f.context
	if contextName == "" {
		if raw, err := clientConfig.RawConfig(); err == nil {
			contextName = raw.CurrentContext
		}
	}
	return client, contextName, nil
}

// addIgnoreIntegrityFlag registers the escape hatch for a damaged archive.
//
// It is hidden because it disables the guarantee that an archive has not been altered. It
// exists to salvage a corrupted file, and cannot bypass authentication on an encrypted
// archive, which simply will not decrypt if tampered with.
func addIgnoreIntegrityFlag(cmd *cobra.Command, dst *bool) {
	const name = "i-know-what-im-doing-ignore-integrity"
	cmd.Flags().BoolVar(dst, name, false, "")
	_ = cmd.Flags().MarkHidden(name)
}
