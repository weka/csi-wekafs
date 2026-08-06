// Package transform rewrites exported objects on their way into a target cluster.
//
// Transforms exist for scenarios (c) and (d): restoring onto a cluster that differs from the
// source, up to and including a different Weka cluster in another geography. They are driven
// entirely by a mapping file the operator writes; nothing is ever inferred. A migration that
// guesses at a filesystem name is worse than one that refuses to run, because the failure
// surfaces as a mount error long after the import reported success.
//
// # Referential integrity
//
// Renaming an object is never a single-object edit. Renaming a PersistentVolume must also
// rewrite the claim's spec.volumeName; renaming a filesystem must rewrite both the volume
// handle and the StorageClass parameter, or the two disagree about where the data lives.
// Every rule therefore rewrites all the places a name appears, driven by one declared
// mapping, so the pieces cannot drift apart.
//
// # Order independence
//
// Rules read the keys they match on from an immutable snapshot of the object as it appeared
// in the archive, and write to the live copy. Without this, a namespace mapping that ran
// before a claim rename would leave the rename unable to find its target. Because every rule
// keys on source identity, the order rules run in cannot change the result.
package transform

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Rule rewrites objects in place. Returning an error aborts the import.
type Rule interface {
	// Name identifies the rule in logs and dry-run output.
	Name() string
	// Apply rewrites obj. original is obj exactly as it appeared in the archive and must be
	// used for every lookup, so the rule is unaffected by what other rules changed. A rule
	// must be a no-op for kinds it does not handle.
	Apply(obj, original *unstructured.Unstructured) error
}

// Change records one rewrite, for dry-run output and logging.
type Change struct {
	Rule   string
	Object string
	Field  string
	From   string
	To     string
}

func (c Change) String() string {
	return fmt.Sprintf("%s: %s %s: %q -> %q", c.Rule, c.Object, c.Field, c.From, c.To)
}

// recorder collects changes and notes which declared mappings were actually used, so that a
// mapping matching nothing can be reported rather than silently ignored.
type recorder struct {
	changes []Change
	used    map[string]bool
}

func newRecorder() *recorder {
	return &recorder{used: map[string]bool{}}
}

func (r *recorder) record(rule, object, field, from, to string) {
	r.changes = append(r.changes, Change{Rule: rule, Object: object, Field: field, From: from, To: to})
}

// use marks a declared mapping key as having matched something.
func (r *recorder) use(key string) { r.used[key] = true }

// Chain applies rules in order.
type Chain struct {
	rules    []Rule
	rec      *recorder
	declared []string
}

// NewChain returns a chain of rules. A chain with no rules is the identity transform.
func NewChain(rules ...Rule) *Chain {
	return &Chain{rules: rules, rec: newRecorder()}
}

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
	if len(c.rules) == 0 {
		return nil
	}
	// One snapshot per object, shared by every rule: see the package documentation.
	original := obj.DeepCopy()
	for _, rule := range c.rules {
		if err := rule.Apply(obj, original); err != nil {
			return fmt.Errorf("%s: %w", rule.Name(), err)
		}
	}
	return nil
}

// Changes returns every rewrite made so far, in the order they happened.
func (c *Chain) Changes() []Change {
	if c.rec == nil {
		return nil
	}
	return c.rec.changes
}

// UnusedMappings returns declared mappings that never matched an object. These are almost
// always typos, and reporting them is what keeps a silently-skipped rename from being
// discovered only when a pod fails to mount.
func (c *Chain) UnusedMappings() []string {
	if c.rec == nil {
		return nil
	}
	var unused []string
	for _, key := range c.declared {
		if !c.rec.used[key] {
			unused = append(unused, key)
		}
	}
	return unused
}
