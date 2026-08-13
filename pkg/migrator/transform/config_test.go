package transform

import (
	"strings"
	"testing"
)

func TestParseConfigFullExample(t *testing.T) {
	cfg, err := ParseConfig([]byte(`
namespaces:
  default: prod
filesystems:
  testfs: replicated-fs
storageClasses:
  sc-dir: sc-dr
persistentVolumes:
  pv-dir: pv-dr
persistentVolumeClaims:
  default/pvc-dir: pvc-dr
secrets:
  csi-wekafs/api-secret:
    name: weka-dr-api
    namespace: weka-dr
    data:
      endpoints: 10.20.30.40:14000
mountOptions: ro,noatime
nodeAffinity:
  key: topology.weka-dr.weka.io/accessible
  values: ["true"]
metadata:
  annotations:
    set:
      migrated-from: prod-us-east
`))
	if err != nil {
		t.Fatalf("ParseConfig returned error: %v", err)
	}
	if cfg.Namespaces["default"] != "prod" {
		t.Errorf("namespaces not parsed: %v", cfg.Namespaces)
	}
	if cfg.Secrets["csi-wekafs/api-secret"].Name != "weka-dr-api" {
		t.Errorf("secrets not parsed: %v", cfg.Secrets)
	}
	if got := cfg.MountOptions.all; len(got) != 2 || got[0] != "ro" {
		t.Errorf("mountOptions = %v, want [ro noatime]", got)
	}
	if cfg.IsEmpty() {
		t.Error("a populated config reports itself empty")
	}
}

// TestParseConfigRejectsUnknownFields is what keeps a misspelled key from being a transform
// the operator believes is happening but is not.
func TestParseConfigRejectsUnknownFields(t *testing.T) {
	_, err := ParseConfig([]byte("namespacs:\n  default: prod\n"))
	if err == nil {
		t.Fatal("a misspelled top-level key was accepted")
	}
	if !strings.Contains(err.Error(), "namespacs") {
		t.Errorf("error does not name the offending key: %v", err)
	}
}

func TestParseConfigRejectsIncoherentCombinations(t *testing.T) {
	for name, yaml := range map[string]string{
		"namespace mapping and single target": "targetNamespace: dr\nnamespaces:\n  default: prod\n",
		"nodeAffinity remove plus key":        "nodeAffinity:\n  remove: true\n  key: topology/x\n",
		"nodeAffinity without key":            "nodeAffinity:\n  values: [\"true\"]\n",
		"pvc key missing namespace":           "persistentVolumeClaims:\n  pvc-dir: pvc-dr\n",
		"pvc target with namespace":           "persistentVolumeClaims:\n  default/pvc-dir: other/pvc-dr\n",
		"secret key missing namespace":        "secrets:\n  api-secret:\n    name: x\n",
		"secret changing nothing":             "secrets:\n  csi-wekafs/api-secret: {}\n",
		"empty mapping target":                "filesystems:\n  testfs: \"\"\n",
		"metadata changing nothing":           "metadata: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig([]byte(yaml)); err == nil {
				t.Errorf("accepted incoherent config:\n%s", yaml)
			}
		})
	}
}

func TestMountOptionsAcceptsAllThreeForms(t *testing.T) {
	for name, yaml := range map[string]string{
		"scalar":   "mountOptions: ro,noatime\n",
		"sequence": "mountOptions: [ro, noatime]\n",
		"mapping":  "mountOptions:\n  pv-dir: ro\n  pv-other: [rw]\n",
		"clear":    "mountOptions: []\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := ParseConfig([]byte(yaml))
			if err != nil {
				t.Fatalf("ParseConfig returned error: %v", err)
			}
			if cfg.MountOptions == nil {
				t.Fatal("mountOptions was not parsed")
			}
		})
	}
}

func TestMountOptionsRejectsNestedMapping(t *testing.T) {
	_, err := ParseConfig([]byte("mountOptions:\n  pv-dir:\n    nested: value\n"))
	if err == nil {
		t.Error("a nested mapping under a volume name was accepted")
	}
}

func TestEmptyConfigIsRecognised(t *testing.T) {
	cfg, err := ParseConfig([]byte("{}\n"))
	if err != nil {
		t.Fatalf("ParseConfig returned error: %v", err)
	}
	if !cfg.IsEmpty() {
		t.Error("an empty config does not report itself empty")
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("PRESENT", "value")

	if got, err := expandEnv("prefix-${PRESENT}-suffix"); err != nil || got != "prefix-value-suffix" {
		t.Errorf("expandEnv = %q, %v", got, err)
	}
	if got, err := expandEnv("no references"); err != nil || got != "no references" {
		t.Errorf("expandEnv = %q, %v", got, err)
	}
	if _, err := expandEnv("${MISSING_VAR_XYZ}"); err == nil {
		t.Error("an unset variable was accepted")
	}
}

func TestDriverNameParsesAndValidates(t *testing.T) {
	cfg, err := ParseConfig([]byte("driverName: weka-infra.weka.io\n"))
	if err != nil {
		t.Fatalf("ParseConfig returned error: %v", err)
	}
	if cfg.DriverName != "weka-infra.weka.io" {
		t.Errorf("driverName = %q", cfg.DriverName)
	}
	if cfg.IsEmpty() {
		t.Error("a config with only driverName reports itself empty")
	}
	if _, err := ParseConfig([]byte("driverName: \"has space\"\n")); err == nil {
		t.Error("a driver name containing whitespace was accepted")
	}
}
