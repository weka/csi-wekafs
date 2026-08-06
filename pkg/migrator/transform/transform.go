// Package transform rewrites exported objects on their way into a target cluster.
//
// v1 ships an empty chain: importing recreates objects exactly as exported, which is all
// scenarios (a) and (b) need. The import path already runs every object through the chain
// (see apply.Options.Transform), so later phases add rules without restructuring anything.
//
// Planned rules, in the order they are likely to be needed:
//
//   - namespace mapping, for claims restored into differently named namespaces
//   - filesystem renaming, which must rewrite the PersistentVolume handle via
//     volumeid.Handle.WithFilesystemName and the StorageClass filesystemName parameter in
//     lockstep, or the volume and the class would disagree about where the data lives
//   - secret endpoint, credential and CA overrides, for a Weka cluster in another network
//   - secret name and namespace remapping, for a driver installed outside csi-wekafs
//   - storageClassName mapping, applied to volumes and claims together
//   - mountOptions replacement, including native-Weka to NFS transport differences
//   - nodeAffinity replacement, covering the topology key as well as its values
//   - innerPath prefix rewriting, when replicated data landed under a different subtree
//   - PersistentVolume renaming, to avoid collisions on the target
//
// Rules are deliberately not inferred. A migration that silently guesses at a filesystem
// name is worse than one that refuses to run, because the failure surfaces as a mount error
// long after the import reported success.
package transform

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Rule rewrites a single object in place. Returning an error aborts the import.
type Rule interface {
	// Name identifies the rule in logs and dry-run output.
	Name() string
	// Apply rewrites obj. It must be a no-op for kinds the rule does not handle.
	Apply(obj *unstructured.Unstructured) error
}

// Chain applies rules in order.
type Chain struct {
	rules []Rule
}

// NewChain returns a chain of rules. A chain with no rules is the v1 identity transform.
func NewChain(rules ...Rule) *Chain { return &Chain{rules: rules} }

// Len reports how many rules the chain holds.
func (c *Chain) Len() int { return len(c.rules) }

// Names lists the rules in application order.
func (c *Chain) Names() []string {
	names := make([]string, 0, len(c.rules))
	for _, rule := range c.rules {
		names = append(names, rule.Name())
	}
	return names
}

// Apply runs every rule against obj.
func (c *Chain) Apply(obj *unstructured.Unstructured) error {
	for _, rule := range c.rules {
		if err := rule.Apply(obj); err != nil {
			return err
		}
	}
	return nil
}
