package wekafs

import (
	"slices"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

// Every volume metric carries the tenant it belongs to, so usage can be grouped by organization.
// The label must never be empty: once in Prometheus a blank label is indistinguishable from one that
// was never set, and Weka credentials leave Organization unset to mean the root organization.
func TestOrganizationLabel(t *testing.T) {
	for name, tc := range map[string]struct {
		client *apiclient.ApiClient
		want   string
	}{
		"named tenant":     {&apiclient.ApiClient{Credentials: apiclient.Credentials{Organization: "tenant-a"}}, "tenant-a"},
		"explicit root":    {&apiclient.ApiClient{Credentials: apiclient.Credentials{Organization: "Root"}}, "Root"},
		"empty means root": {&apiclient.ApiClient{Credentials: apiclient.Credentials{}}, apiclient.RootOrganizationName},
		"no client at all": {nil, apiclient.RootOrganizationName},
	} {
		if got := organizationLabel(tc.client); got != tc.want {
			t.Errorf("%s: organizationLabel() = %q, want %q", name, got, tc.want)
		}
	}
}

func metricForLabelTest(org string, withClaimRef bool) (*MetricsServer, *VolumeMetric) {
	ms := &MetricsServer{driver: &WekaFsDriver{name: "csi.weka.io"}}
	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc-test"},
		Spec:       v1.PersistentVolumeSpec{StorageClassName: "sc-weka-dir"},
	}
	if withClaimRef {
		pv.Spec.ClaimRef = &v1.ObjectReference{Name: "claim", Namespace: "ns", UID: "uid-1"}
	}
	return ms, &VolumeMetric{
		persistentVolume: pv,
		volume:           &Volume{FilesystemName: "default"},
		apiClient:        &apiclient.ApiClient{Credentials: apiclient.Credentials{Organization: org}},
	}
}

// The label names and the values are built in different files, and a mismatch surfaces only at
// scrape time, as an error from WithLabelValues. So call the real builder and check it against the
// real label list, rather than against a count written down twice.
func TestBuiltLabelValuesMatchTheLabelList(t *testing.T) {
	for _, withClaimRef := range []bool{true, false} {
		ms, metric := metricForLabelTest("tenant-a", withClaimRef)
		values := ms.createPrometheusLabelsForMetric(metric)

		if len(values) != len(LabelsForCsiVolumes) {
			t.Fatalf("claimRef=%v: builder produced %d values for %d labels %v",
				withClaimRef, len(values), len(LabelsForCsiVolumes), LabelsForCsiVolumes)
		}

		// Positional, not just counted: inserting a label in one place and its value in another
		// keeps the count right while silently mislabelling every series.
		orgIdx := slices.Index(LabelsForCsiVolumes, "organization")
		if orgIdx < 0 {
			t.Fatal("organization label is missing - volume usage cannot be grouped by tenant")
		}
		if values[orgIdx] != "tenant-a" {
			t.Errorf("claimRef=%v: organization is label %d but the builder put %q there",
				withClaimRef, orgIdx, values[orgIdx])
		}
		if got := values[slices.Index(LabelsForCsiVolumes, "filesystem_name")]; got != "default" {
			t.Errorf("claimRef=%v: filesystem_name position holds %q", withClaimRef, got)
		}
		if got := values[slices.Index(LabelsForCsiVolumes, "pv_name")]; got != "pvc-test" {
			t.Errorf("claimRef=%v: pv_name position holds %q", withClaimRef, got)
		}
	}
}

// The end-to-end check: this is precisely what the metrics server does on every report, and what
// panics at scrape time if the arity is wrong.
func TestBuiltLabelValuesAreAcceptedByTheRealMetric(t *testing.T) {
	vec := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Namespace: MetricsPrefix, Subsystem: VolumesSubsystem, Name: "test_capacity_bytes"},
		LabelsForCsiVolumes)

	ms, metric := metricForLabelTest("", true) // empty org exercises the Root fallback
	values := ms.createPrometheusLabelsForMetric(metric)

	g, err := vec.GetMetricWithLabelValues(values...)
	if err != nil {
		t.Fatalf("the real metric rejected the builder's values: %v", err)
	}
	g.Set(1)

	if got := values[slices.Index(LabelsForCsiVolumes, "organization")]; got != apiclient.RootOrganizationName {
		t.Errorf("empty organization exported as %q, want %q", got, apiclient.RootOrganizationName)
	}
}
