package wekafs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// TestCsiVolumeLabelValuesMatchLabels is the invariant that fails silently everywhere else: a
// Prometheus vector panics at runtime, not at compile time, when the number of values does not match
// the number of labels. Since these two are edited in different places - a label added to the list,
// a value appended in the builder - they drift easily, and the cost lands on whoever is provisioning
// volumes rather than on whoever made the change.
func TestCsiVolumeLabelValuesMatchLabels(t *testing.T) {
	for _, tc := range []struct {
		name string
		pv   *v1.PersistentVolume
	}{
		{"bound volume", pvWithClaim()},
		{"unbound volume, no claim ref", pvWithoutClaim()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := csiVolumeLabelValues("csi.weka.io", tc.pv, "guid", "fs", "dir/v1", "Root")
			assert.Len(t, values, len(LabelsForCsiVolumes),
				"label values must line up with LabelsForCsiVolumes, or every metric using them panics")
		})
	}
}

// TestCsiVolumeLabelValuesOrder pins the values to their label names. Order is the whole contract
// here: a swap between two string labels produces a metric that is wrong rather than one that fails.
func TestCsiVolumeLabelValuesOrder(t *testing.T) {
	values := csiVolumeLabelValues("csi.weka.io", pvWithClaim(), "cluster-guid", "myfs", "dir/v1", "Tenant1")
	require.Len(t, values, len(LabelsForCsiVolumes))

	got := map[string]string{}
	for i, name := range LabelsForCsiVolumes {
		got[name] = values[i]
	}

	assert.Equal(t, "csi.weka.io", got["csi_driver_name"])
	assert.Equal(t, "pv-1", got["pv_name"])
	assert.Equal(t, "cluster-guid", got["cluster_guid"])
	assert.Equal(t, "sc-weka", got["storage_class_name"])
	assert.Equal(t, "myfs", got["filesystem_name"])
	assert.Equal(t, "dir/v1", got["volume_type"])
	assert.Equal(t, "Tenant1", got["organization"])
	assert.Equal(t, "claim-1", got["pvc_name"])
	assert.Equal(t, "team-a", got["pvc_namespace"])
	assert.Equal(t, "uid-1", got["pvc_uid"])
	assert.Equal(t, "weka-secrets/api-creds", got["secret_name"])
}

// TestCsiVolumeLabelValuesUnbound covers a volume that was provisioned but never bound. It has no
// claim, and the claim labels must be blank rather than absent - a shorter slice would panic.
func TestCsiVolumeLabelValuesUnbound(t *testing.T) {
	values := csiVolumeLabelValues("csi.weka.io", pvWithoutClaim(), "guid", "fs", "dir/v1", "Root")
	require.Len(t, values, len(LabelsForCsiVolumes))

	got := map[string]string{}
	for i, name := range LabelsForCsiVolumes {
		got[name] = values[i]
	}
	assert.Empty(t, got["pvc_name"])
	assert.Empty(t, got["pvc_namespace"])
	assert.Empty(t, got["pvc_uid"])
	assert.Equal(t, "pv-2", got["pv_name"], "the volume itself is still identified")
}

func TestSecretRefLabel(t *testing.T) {
	assert.Equal(t, "weka-secrets/api-creds", secretRefLabel(pvWithClaim()))
	assert.Empty(t, secretRefLabel(pvWithoutClaim()),
		"a volume with no Secret ref must label blank, not panic")
	assert.Empty(t, secretRefLabel(&v1.PersistentVolume{}),
		"a volume with no CSI spec at all must label blank")
}

// TestLabelsForCsiVolumesAreUnique guards against a duplicate label name, which Prometheus rejects
// at registration - turning a typo into a process that fails to start.
func TestLabelsForCsiVolumesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, l := range LabelsForCsiVolumes {
		assert.False(t, seen[l], "duplicate label %q", l)
		seen[l] = true
	}
}

func pvWithClaim() *v1.PersistentVolume {
	pv := pvWithoutClaim()
	pv.Name = "pv-1"
	pv.Spec.ClaimRef = &v1.ObjectReference{Name: "claim-1", Namespace: "team-a", UID: types.UID("uid-1")}
	pv.Spec.CSI.NodeStageSecretRef = &v1.SecretReference{Name: "api-creds", Namespace: "weka-secrets"}
	return pv
}

func pvWithoutClaim() *v1.PersistentVolume {
	return &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-2"},
		Spec: v1.PersistentVolumeSpec{
			StorageClassName: "sc-weka",
			PersistentVolumeSource: v1.PersistentVolumeSource{
				CSI: &v1.CSIPersistentVolumeSource{Driver: "csi.weka.io", VolumeHandle: "weka/v2/fs/dir"},
			},
		},
	}
}
