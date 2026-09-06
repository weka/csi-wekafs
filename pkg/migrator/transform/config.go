package transform

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// Config is the mapping file that drives a transformed import.
//
// Every mapping is keyed by the object's identity **as it appears in the archive**, never by
// what it will become. Use `weka-csi-migrator list` to read those identities.
type Config struct {
	// TargetNamespace moves every claim into one namespace. Mutually exclusive with
	// Namespaces.
	TargetNamespace string `json:"targetNamespace,omitempty"`
	// Namespaces maps source namespace to target namespace.
	//
	// This deliberately does not move Secrets: the Weka API secret lives in the driver's
	// namespace, not a workload namespace, and sweeping it along with a workload mapping
	// would point volumes at a secret that does not exist. Remap secrets explicitly.
	Namespaces map[string]string `json:"namespaces,omitempty"`

	// Filesystems maps source Weka filesystem name to target. Applied to volume handles,
	// volume attributes and StorageClass parameters together.
	Filesystems map[string]string `json:"filesystems,omitempty"`

	// DriverName is the CSI driver name on the target cluster, applied to every
	// PersistentVolume and StorageClass in the archive.
	//
	// It is a single value rather than a mapping because an archive holds exactly one
	// driver by construction: export selects volumes by --driver-name. Restating the source
	// name would be busywork with an extra chance to get it wrong.
	//
	// The chart's csiDriverName is overridable, so a target cluster can perfectly well run
	// the driver under a different name; a volume naming a driver the target does not have
	// stays Pending with no node able to stage it.
	DriverName string `json:"driverName,omitempty"`

	// StorageClasses maps source StorageClass name to target, on the class itself and on
	// every volume and claim that references it.
	StorageClasses map[string]string `json:"storageClasses,omitempty"`

	// PersistentVolumes maps source PV name to target, on the volume and on the claim
	// bound to it.
	PersistentVolumes map[string]string `json:"persistentVolumes,omitempty"`

	// PersistentVolumeClaims maps "<source-namespace>/<name>" to a new name, on the claim
	// and on the volume's claimRef. The value is a bare name; use Namespaces or
	// TargetNamespace to move it.
	PersistentVolumeClaims map[string]string `json:"persistentVolumeClaims,omitempty"`

	// Secrets maps "<source-namespace>/<name>" to overrides, applied to the Secret itself
	// and to every reference from a PersistentVolume or StorageClass.
	Secrets map[string]SecretOverride `json:"secrets,omitempty"`

	// MountOptions replaces spec.mountOptions on PersistentVolumes.
	MountOptions *MountOptionsSpec `json:"mountOptions,omitempty"`

	// NodeAffinity replaces spec.nodeAffinity on PersistentVolumes.
	NodeAffinity *NodeAffinitySpec `json:"nodeAffinity,omitempty"`

	// Metadata edits annotations and labels on every object in the archive.
	Metadata *MetadataSpec `json:"metadata,omitempty"`
}

// SecretOverride redirects a Secret and rewrites its contents.
type SecretOverride struct {
	// Name and Namespace relocate the Secret. Empty means unchanged.
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	// Data sets keys to plaintext values, which are base64-encoded on the way in — write
	// `endpoints: 10.0.0.1:14000`, not the encoded form. Values may reference environment
	// variables as ${VAR}, which is how a password stays out of the mapping file.
	Data map[string]string `json:"data,omitempty"`
	// RemoveData deletes keys outright, for a target that does not use them.
	RemoveData []string `json:"removeData,omitempty"`
}

// NodeAffinitySpec replaces the scheduling constraint that keeps a volume on nodes running a
// healthy Weka client. The topology key is part of the driver's configuration, so a target
// cluster with a different install needs the key changed, not only its values.
type NodeAffinitySpec struct {
	// Remove drops spec.nodeAffinity entirely. Mutually exclusive with the fields below.
	Remove bool `json:"remove,omitempty"`
	// Key is the node label to match, e.g. topology.weka-infra.weka.io/accessible.
	Key string `json:"key,omitempty"`
	// Operator defaults to In.
	Operator string `json:"operator,omitempty"`
	// Values are the accepted label values.
	Values []string `json:"values,omitempty"`
}

// MetadataSpec edits annotations and labels.
type MetadataSpec struct {
	Annotations *MapEdit `json:"annotations,omitempty"`
	Labels      *MapEdit `json:"labels,omitempty"`
	// Kinds restricts the edits to these kinds. Empty means every object.
	Kinds []string `json:"kinds,omitempty"`
}

// MapEdit describes changes to one metadata map.
type MapEdit struct {
	// Set adds or overwrites entries. Values may reference ${VAR}.
	Set map[string]string `json:"set,omitempty"`
	// Remove deletes keys.
	Remove []string `json:"remove,omitempty"`
	// Rename moves a value from one key to another, preserving it.
	Rename map[string]string `json:"rename,omitempty"`
}

// MountOptionsSpec is either one set of options for every volume, or per-volume sets.
//
// The YAML accepts a scalar ("ro,noatime"), a sequence, or a mapping from PersistentVolume
// name to either of those. An explicit empty sequence clears the options.
type MountOptionsSpec struct {
	// all applies to every PersistentVolume. nil when byPV is in use.
	all []string
	// allSet distinguishes "not configured" from "configured as empty", which is what makes
	// `mountOptions: []` able to clear existing options rather than being ignored.
	allSet bool
	// byPV maps PersistentVolume name to its options.
	byPV map[string][]string
}

// UnmarshalJSON accepts the scalar, sequence and mapping forms.
func (m *MountOptionsSpec) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	switch {
	case trimmed == "null":
		return nil

	case strings.HasPrefix(trimmed, `"`):
		var single string
		if err := json.Unmarshal(data, &single); err != nil {
			return err
		}
		m.all, m.allSet = splitOptions(single), true
		return nil

	case strings.HasPrefix(trimmed, "["):
		var list []string
		if err := json.Unmarshal(data, &list); err != nil {
			return err
		}
		m.all, m.allSet = list, true
		return nil

	case strings.HasPrefix(trimmed, "{"):
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		m.byPV = make(map[string][]string, len(raw))
		for pv, value := range raw {
			var nested MountOptionsSpec
			if err := nested.UnmarshalJSON(value); err != nil {
				return fmt.Errorf("mountOptions for %q: %w", pv, err)
			}
			if nested.byPV != nil {
				return fmt.Errorf("mountOptions for %q must be a string or a list, not a mapping", pv)
			}
			m.byPV[pv] = nested.all
		}
		return nil

	default:
		return fmt.Errorf("mountOptions must be a string, a list, or a mapping of volume name to either")
	}
}

// splitOptions turns "ro,noatime" into discrete options, which is how an operator naturally
// writes a mount option string even though the field is a list.
func splitOptions(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// namespacedKey matches "<namespace>/<name>".
var namespacedKey = regexp.MustCompile(`^[^/]+/[^/]+$`)

// LoadConfig reads and validates a mapping file.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading transform file %s: %w", path, err)
	}
	return ParseConfig(raw)
}

// ParseConfig decodes and validates a mapping file.
//
// Decoding is strict: an unrecognised field is an error rather than a silent no-op, because
// a misspelled key would otherwise mean a transform the operator believes is happening but
// is not.
func ParseConfig(raw []byte) (*Config, error) {
	var cfg Config
	if err := yaml.UnmarshalStrict(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parsing transform file: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate reports configuration that cannot be applied coherently.
func (c *Config) Validate() error {
	if c.TargetNamespace != "" && len(c.Namespaces) > 0 {
		return fmt.Errorf("targetNamespace and namespaces are mutually exclusive: use one or the other")
	}

	for field, mapping := range map[string]map[string]string{
		"namespaces":     c.Namespaces,
		"filesystems":    c.Filesystems,
		"storageClasses": c.StorageClasses,
	} {
		if err := validateSimpleMapping(field, mapping); err != nil {
			return err
		}
	}
	if err := validateSimpleMapping("persistentVolumes", c.PersistentVolumes); err != nil {
		return err
	}

	if c.DriverName != "" && strings.ContainsAny(c.DriverName, " \t") {
		return fmt.Errorf("driverName %q may not contain whitespace", c.DriverName)
	}

	for key, target := range c.PersistentVolumeClaims {
		if !namespacedKey.MatchString(key) {
			return fmt.Errorf("persistentVolumeClaims key %q must be \"<namespace>/<name>\"", key)
		}
		if target == "" {
			return fmt.Errorf("persistentVolumeClaims[%q] has an empty target name", key)
		}
		if strings.Contains(target, "/") {
			return fmt.Errorf("persistentVolumeClaims[%q] target %q must be a bare name; "+
				"use namespaces or targetNamespace to move a claim", key, target)
		}
	}

	for key, override := range c.Secrets {
		if !namespacedKey.MatchString(key) {
			return fmt.Errorf("secrets key %q must be \"<namespace>/<name>\"", key)
		}
		if override.Name == "" && override.Namespace == "" && len(override.Data) == 0 && len(override.RemoveData) == 0 {
			return fmt.Errorf("secrets[%q] changes nothing", key)
		}
		if strings.Contains(override.Name, "/") {
			return fmt.Errorf("secrets[%q].name %q must be a bare name", key, override.Name)
		}
	}

	if c.NodeAffinity != nil {
		na := c.NodeAffinity
		switch {
		case na.Remove && (na.Key != "" || len(na.Values) > 0):
			return fmt.Errorf("nodeAffinity.remove cannot be combined with key or values")
		case !na.Remove && na.Key == "":
			return fmt.Errorf("nodeAffinity needs a key, or remove: true")
		case !na.Remove && len(na.Values) == 0 && operatorNeedsValues(na.Operator):
			return fmt.Errorf("nodeAffinity with operator %q needs at least one value", na.effectiveOperator())
		}
	}

	if c.Metadata != nil {
		for name, edit := range map[string]*MapEdit{
			"annotations": c.Metadata.Annotations,
			"labels":      c.Metadata.Labels,
		} {
			if edit == nil {
				continue
			}
			for from, to := range edit.Rename {
				if to == "" {
					return fmt.Errorf("metadata.%s.rename[%q] has an empty target key", name, from)
				}
			}
		}
		if c.Metadata.Annotations == nil && c.Metadata.Labels == nil {
			return fmt.Errorf("metadata changes nothing")
		}
	}

	return nil
}

func validateSimpleMapping(field string, mapping map[string]string) error {
	for from, to := range mapping {
		if from == "" {
			return fmt.Errorf("%s has an empty source key", field)
		}
		if to == "" {
			return fmt.Errorf("%s[%q] has an empty target", field, from)
		}
	}
	return nil
}

func (n *NodeAffinitySpec) effectiveOperator() string {
	if n.Operator == "" {
		return "In"
	}
	return n.Operator
}

// operatorNeedsValues reports whether a node selector operator requires values. Exists and
// DoesNotExist take none; everything else does.
func operatorNeedsValues(operator string) bool {
	switch operator {
	case "Exists", "DoesNotExist":
		return false
	default:
		return true
	}
}

// IsEmpty reports whether the config would change nothing.
func (c *Config) IsEmpty() bool {
	return c.TargetNamespace == "" &&
		len(c.Namespaces) == 0 &&
		len(c.Filesystems) == 0 &&
		c.DriverName == "" &&
		len(c.StorageClasses) == 0 &&
		len(c.PersistentVolumes) == 0 &&
		len(c.PersistentVolumeClaims) == 0 &&
		len(c.Secrets) == 0 &&
		c.MountOptions == nil &&
		c.NodeAffinity == nil &&
		c.Metadata == nil
}

// declaredKeys lists every mapping key, so the chain can report the ones that matched
// nothing.
func (c *Config) declaredKeys() []string {
	var keys []string
	add := func(field string, mapping map[string]string) {
		for from := range mapping {
			keys = append(keys, field+"["+from+"]")
		}
	}
	add("namespaces", c.Namespaces)
	add("filesystems", c.Filesystems)
	add("storageClasses", c.StorageClasses)
	add("persistentVolumes", c.PersistentVolumes)
	add("persistentVolumeClaims", c.PersistentVolumeClaims)
	for key := range c.Secrets {
		keys = append(keys, "secrets["+key+"]")
	}
	if c.DriverName != "" {
		keys = append(keys, "driverName")
	}
	if c.MountOptions != nil {
		for pv := range c.MountOptions.byPV {
			keys = append(keys, "mountOptions["+pv+"]")
		}
	}
	sort.Strings(keys)
	return keys
}

// expandEnv resolves ${VAR} references.
//
// An unset variable is an error rather than an empty string: silently writing an empty
// password produces a Secret that fails authentication at first mount, with nothing to
// point at the cause.
func expandEnv(value string) (string, error) {
	var missing []string
	expanded := envRef.ReplaceAllStringFunc(value, func(match string) string {
		name := envRef.FindStringSubmatch(match)[1]
		resolved, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return ""
		}
		return resolved
	})
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("environment variable(s) not set: %s", strings.Join(missing, ", "))
	}
	return expanded, nil
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
