/*
Copyright 2019-2025 Weka.io LTD and The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package wekafs

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// fakeHealthReconcilerManager satisfies ctrl.Manager for reconcileOnce's needs and nothing else:
// listDriverPersistentVolumes only calls GetClient(), and apiClientFromPersistentVolume's Secret
// read only calls GetAPIReader(). Every other method is promoted from the embedded nil interface
// and would panic if ever called, which nothing exercised by these tests does.
type fakeHealthReconcilerManager struct {
	ctrl.Manager
	client runtimeclient.Client
}

func (m *fakeHealthReconcilerManager) GetClient() runtimeclient.Client    { return m.client }
func (m *fakeHealthReconcilerManager) GetAPIReader() runtimeclient.Reader { return m.client }

// healthReconcilerSecretName/Namespace identify the Secret that points a PV's credentials at the
// hermetic fake Weka API server TestMain already started for this package (see volume_test.go). The
// ControllerServer built below disables UseNfs/AllowNfsFailback, so resolving it never tries to
// register an NFS client group against the fixture.
const (
	healthReconcilerSecretName      = "weka-api-secret"
	healthReconcilerSecretNamespace = "csi-wekafs"
)

func healthReconcilerSecretRef() *v1.SecretReference {
	return &v1.SecretReference{Name: healthReconcilerSecretName, Namespace: healthReconcilerSecretNamespace}
}

func healthReconcilerSecret() *v1.Secret {
	return &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: healthReconcilerSecretName, Namespace: healthReconcilerSecretNamespace},
		Data: map[string][]byte{
			"endpoints":    []byte(endpoint),
			"username":     []byte(creds.Username),
			"password":     []byte(creds.Password),
			"organization": []byte(creds.Organization),
			"scheme":       []byte(creds.HttpScheme),
		},
	}
}

// newHealthReconcilerTestServer builds a ControllerServer wired to a fake controller-runtime client
// (so PersistentVolumes and the Secret come from in-memory objects) and the package's shared fake
// Weka API server (so describeVolume's REST calls are answered without a real cluster). It returns
// the ControllerServer, a fresh cache, and the fake client so a test can mutate the PV list between
// sweeps.
func newHealthReconcilerTestServer(t *testing.T, objs ...runtimeclient.Object) (*ControllerServer, *volumeConditionCache, runtimeclient.Client) {
	t.Helper()

	fakeClient := fake.NewClientBuilder().WithObjects(objs...).Build()
	mgr := &fakeHealthReconcilerManager{client: fakeClient}

	driverConfig := NewDriverConfig(DriverConfigOptions{
		DynamicVolPath:     "csi-volumes",
		VolumePrefix:       "csi-vol-",
		SnapshotPrefix:     "csi-snap-",
		SeedSnapshotPrefix: "csi-seed-snap-",
		Version:            "v1",

		AllowAutoFsCreation:              true,
		AllowAutoFsExpansion:             true,
		AllowSnapshotsOfDirectoryVolumes: true,
		AllowInsecureHttps:               true,
		AlwaysAllowSnapshotVolumes:       true,
		AllowProtocolContainers:          true,
		AllowEncryptionWithoutKms:        true,

		SuppressSnapshotSupport:      true,
		SuppressVolumeCloneSupport:   true,
		AdvertiseVolumeHealthSupport: true,

		MaxCreateVolumeReqs:        1,
		MaxDeleteVolumeReqs:        1,
		MaxExpandVolumeReqs:        1,
		MaxCreateSnapshotReqs:      1,
		MaxDeleteSnapshotReqs:      1,
		MaxNodePublishVolumeReqs:   1,
		MaxNodeUnpublishVolumeReqs: 1,

		GrpcRequestTimeoutSeconds:     10,
		HealthProbeWekaTimeoutSeconds: 5,

		// Deliberately off: reconcileOnce never mounts anything, and skipping NFS means credential
		// resolution never tries to register a client group against the fixture.
		AllowNfsFailback: false,
		UseNfs:           false,
	})

	driver, err := NewWekaFsDriver("csi.weka.io", "localhost", "unix://tmp/csi.sock", 10, "v1.0", "", CsiModeAll, false, driverConfig)
	if err != nil {
		t.Fatalf("failed to create driver: %v", err)
	}
	// listDriverPersistentVolumes filters by cs.getConfig().GetDriver().name, which is nil until
	// something calls SetDriver - main.go does this for the real binary, so a reconciler test must
	// do it too, or the sweep panics on a nil dereference.
	driverConfig.SetDriver(driver)

	cs := NewControllerServer("localhost", driver.api, nil, driverConfig, mgr)
	cache := newVolumeConditionCache()
	return cs, cache, fakeClient
}

// healthReconcilerTestPV builds a PersistentVolume of this driver for the reconciler to probe.
// secretRef nil means the volume has no way to authenticate, which describeVolume reports as
// "unknown" rather than an error.
func healthReconcilerTestPV(name, handle, storageClass string, secretRef *v1.SecretReference) *v1.PersistentVolume {
	return &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1.PersistentVolumeSpec{
			StorageClassName: storageClass,
			ClaimRef:         &v1.ObjectReference{Name: "pvc-" + name, Namespace: "default", UID: types.UID(name)},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				CSI: &v1.CSIPersistentVolumeSource{
					Driver:                    "csi.weka.io",
					VolumeHandle:              handle,
					ControllerExpandSecretRef: secretRef,
				},
			},
		},
	}
}

// statusFor fetches the weka_csi_volume_health_status value the reconciler set for a volume, using
// the exact label set the cache recorded for it - the same slice describeVolume/reconcileOnce built
// and passed to WithLabelValues - rather than reconstructing the labels by hand, so the assertion
// does not have to guess at values like cluster_guid that only the fake Weka cluster knows.
func statusFor(t *testing.T, cache *volumeConditionCache, handle string) float64 {
	t.Helper()
	entry, ok := cache.lookup(handle)
	if !ok {
		t.Fatalf("expected %s to be cached", handle)
	}
	if entry.labels == nil {
		t.Fatalf("expected %s to have resolved labels", handle)
	}
	return testutil.ToFloat64(controllerMetrics.VolumeHealth.Status.WithLabelValues(entry.labels...))
}

// TestReconcileOnceReportsVolumeHealthMetrics drives a real sweep - listing PersistentVolumes
// through a fake controller-runtime client and probing them against the package's shared fake Weka
// API server - over a mix of outcomes, and asserts on the Prometheus series the sweep produces
// rather than merely on the internal cache. This is the path a green suite could otherwise pass
// without ever touching: metric objects existing proves nothing about whether reconcileOnce feeds
// them.
func TestReconcileOnceReportsVolumeHealthMetrics(t *testing.T) {
	const driverName = "csi.weka.io"

	healthyPV := healthReconcilerTestPV("pv-healthy", "weka/v2/default", "wekafs-sc", healthReconcilerSecretRef())
	abnormalPV := healthReconcilerTestPV("pv-abnormal", "weka/v2/does-not-exist", "wekafs-sc", healthReconcilerSecretRef())
	unknownPV := healthReconcilerTestPV("pv-unknown", "weka/v2/no-credentials", "wekafs-sc", nil)
	// A secret reference to a Secret that was never created: readSecret's Get fails, so this is a
	// probe error ("failed"), distinct from "unknown" (no reference at all).
	failedPV := healthReconcilerTestPV("pv-failed", "weka/v2/failed-vol", "wekafs-sc",
		&v1.SecretReference{Name: "missing-secret", Namespace: healthReconcilerSecretNamespace})

	cs, cache, _ := newHealthReconcilerTestServer(t, healthReconcilerSecret(), healthyPV, abnormalPV, unknownPV, failedPV)
	reconciler := newVolumeHealthReconciler(cs, cache)

	reconciler.reconcileOnce(context.Background())

	if got := statusFor(t, cache, "weka/v2/default"); got != volumeHealthStatusHealthy {
		t.Errorf("expected healthy volume status %v, got %v", float64(volumeHealthStatusHealthy), got)
	}
	if got := statusFor(t, cache, "weka/v2/does-not-exist"); got != volumeHealthStatusAbnormal {
		t.Errorf("expected abnormal volume status %v, got %v", float64(volumeHealthStatusAbnormal), got)
	}

	// The unknown volume never resolved an API client, so it never got a label set at all - there is
	// nothing to assert Status against, and the aggregate tally below is what covers it.
	if entry, ok := cache.lookup("weka/v2/no-credentials"); !ok || entry.known || entry.labels != nil {
		t.Fatalf("expected the credential-less volume to be cached as unknown with no labels, got ok=%v entry=%+v", ok, entry)
	}

	if got := testutil.ToFloat64(controllerMetrics.VolumeHealth.Volumes.WithLabelValues(driverName, "healthy")); got != 1 {
		t.Errorf("expected 1 healthy volume, got %v", got)
	}
	if got := testutil.ToFloat64(controllerMetrics.VolumeHealth.Volumes.WithLabelValues(driverName, "abnormal")); got != 1 {
		t.Errorf("expected 1 abnormal volume, got %v", got)
	}
	if got := testutil.ToFloat64(controllerMetrics.VolumeHealth.Volumes.WithLabelValues(driverName, "unknown")); got != 1 {
		t.Errorf("expected 1 unknown volume, got %v", got)
	}
	if got := testutil.ToFloat64(controllerMetrics.VolumeHealth.Volumes.WithLabelValues(driverName, "failed")); got != 1 {
		t.Errorf("expected 1 failed volume, got %v", got)
	}

	if got := testutil.ToFloat64(controllerMetrics.VolumeHealth.LastSweepTimestamp.WithLabelValues(driverName)); got <= 0 {
		t.Errorf("expected a positive last-sweep timestamp, got %v", got)
	}
	if count := testutil.CollectAndCount(controllerMetrics.VolumeHealth.SweepDuration, MetricsPrefix+"_volume_health_sweep_duration_seconds"); count == 0 {
		t.Error("expected the sweep duration histogram to have observed a sample")
	}
}

// TestReconcileOncePrunesRemovedVolumeSeries is the correctness requirement that matters most: a
// volume that disappears between sweeps must have its weka_csi_volume_health_status series deleted,
// or a churning fleet leaks a series per deleted volume forever. It asserts on the total series
// count the collector reports, not on the cache, since the cache shrinking proves nothing about
// whether the Prometheus series was ever cleaned up.
func TestReconcileOncePrunesRemovedVolumeSeries(t *testing.T) {
	stayingPV := healthReconcilerTestPV("pv-staying", "weka/v2/default", "wekafs-sc", healthReconcilerSecretRef())
	doomedPV := healthReconcilerTestPV("pv-doomed", "weka/v2/doomed", "wekafs-sc", healthReconcilerSecretRef())

	cs, cache, fakeClient := newHealthReconcilerTestServer(t, healthReconcilerSecret(), stayingPV, doomedPV)
	reconciler := newVolumeHealthReconciler(cs, cache)

	reconciler.reconcileOnce(context.Background())
	before := testutil.CollectAndCount(controllerMetrics.VolumeHealth.Status)
	if before == 0 {
		t.Fatal("expected the first sweep to produce at least one volume-health series")
	}
	if _, ok := cache.lookup("weka/v2/doomed"); !ok {
		t.Fatal("expected the doomed volume to be cached after the first sweep")
	}

	// The doomed PV disappears: delete it from Kubernetes and sweep again.
	if err := fakeClient.Delete(context.Background(), doomedPV); err != nil {
		t.Fatalf("failed to delete PV: %v", err)
	}
	reconciler.reconcileOnce(context.Background())

	after := testutil.CollectAndCount(controllerMetrics.VolumeHealth.Status)
	if after != before-1 {
		t.Fatalf("expected exactly one volume-health series to be pruned (from %d to %d), got %d remaining",
			before, before-1, after)
	}
	if _, ok := cache.lookup("weka/v2/doomed"); ok {
		t.Fatal("expected the doomed volume to be evicted from the cache too")
	}
	if _, ok := cache.lookup("weka/v2/default"); !ok {
		t.Fatal("expected the staying volume to remain cached")
	}
}
