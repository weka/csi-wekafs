package wekafs

import (
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestPreferredSecretRef(t *testing.T) {
	ref := func(name string) *v1.SecretReference {
		return &v1.SecretReference{Name: name, Namespace: "csi-wekafs"}
	}

	for _, tc := range []struct {
		name     string
		source   *v1.CSIPersistentVolumeSource
		expected string
	}{
		{
			name:   "no CSI source",
			source: nil,
		},
		{
			name:   "no refs at all",
			source: &v1.CSIPersistentVolumeSource{Driver: "csi.weka.io"},
		},
		{
			name: "controller expand ref wins over node refs",
			source: &v1.CSIPersistentVolumeSource{
				ControllerExpandSecretRef:  ref("expand"),
				ControllerPublishSecretRef: ref("publish"),
				NodeStageSecretRef:         ref("stage"),
			},
			expected: "expand",
		},
		{
			name: "falls back to node stage ref",
			source: &v1.CSIPersistentVolumeSource{
				NodeStageSecretRef:   ref("stage"),
				NodePublishSecretRef: ref("node-publish"),
			},
			expected: "stage",
		},
		{
			name: "ref without namespace is unusable",
			source: &v1.CSIPersistentVolumeSource{
				ControllerExpandSecretRef: &v1.SecretReference{Name: "expand"},
				NodeStageSecretRef:        ref("stage"),
			},
			expected: "stage",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pv := &v1.PersistentVolume{
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeSource: v1.PersistentVolumeSource{CSI: tc.source},
				},
			}
			got := preferredSecretRef(pv)
			if tc.expected == "" {
				if got != nil {
					t.Fatalf("expected no secret ref, got %s/%s", got.Namespace, got.Name)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected secret ref %s, got none", tc.expected)
			}
			if got.Name != tc.expected {
				t.Fatalf("expected secret ref %s, got %s", tc.expected, got.Name)
			}
		})
	}
}

func TestPvCapacityBytes(t *testing.T) {
	if got := pvCapacityBytes(&v1.PersistentVolume{}); got != 0 {
		t.Fatalf("expected 0 for PV without capacity, got %d", got)
	}
	pv := &v1.PersistentVolume{
		Spec: v1.PersistentVolumeSpec{
			Capacity: v1.ResourceList{v1.ResourceStorage: resource.MustParse("1Gi")},
		},
	}
	if got := pvCapacityBytes(pv); got != 1024*1024*1024 {
		t.Fatalf("expected 1Gi in bytes, got %d", got)
	}
}

func TestSecretCache(t *testing.T) {
	sc := newSecretCache(time.Hour)
	if _, ok := sc.lookup("csi-wekafs/api"); ok {
		t.Fatal("expected a miss on an empty cache")
	}

	sc.store("csi-wekafs/api", map[string]string{"username": "admin"})
	secrets, ok := sc.lookup("csi-wekafs/api")
	if !ok {
		t.Fatal("expected a hit for a freshly stored key")
	}
	if secrets["username"] != "admin" {
		t.Fatalf("unexpected secret contents: %v", secrets)
	}
	if _, ok := sc.lookup("csi-wekafs/other"); ok {
		t.Fatal("expected a miss for a key that was never stored")
	}

	// A zero TTL makes every entry stale on arrival, so every lookup is a miss.
	expired := newSecretCache(0)
	expired.store("csi-wekafs/api", map[string]string{"username": "admin"})
	if _, ok := expired.lookup("csi-wekafs/api"); ok {
		t.Fatal("expected a miss for a stale entry")
	}
}

func TestAbnormalVolumeHealth(t *testing.T) {
	health := abnormalVolumeHealth("filesystem %s does not exist", "myfs")
	if !health.Abnormal {
		t.Fatal("expected health to be abnormal")
	}
	if health.Message != "filesystem myfs does not exist" {
		t.Fatalf("unexpected message: %s", health.Message)
	}
	if health.Capacity != 0 {
		t.Fatalf("expected no capacity on an abnormal volume, got %d", health.Capacity)
	}
}
