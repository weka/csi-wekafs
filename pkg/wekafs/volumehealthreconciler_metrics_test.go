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
	"time"

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

// healthMetricLabels returns a distinct, well-formed LabelsForCsiVolumes value for handle, so tests
// below don't need a real PersistentVolume or fake Weka cluster just to give the Status gauge
// something to key on.
func healthMetricLabels(handle string) []string {
	return []string{"csi.weka.io", "pv-" + handle, "guid-" + handle, "wekafs-sc", "fs-" + handle, "dir", "default", "pvc-" + handle, "default", "uid-" + handle}
}

// TestDeleteVolumeForgetsHealthMetricsImmediately is the regression test for the fix this change
// makes: without it, a deleted volume's weka_csi_volume_health_status series survives until the next
// reconciler sweep evicts it via retainOnly, which can lag by a full volumeHealthReconcileInterval.
// forgetVolumeHealthMetrics is the exact call DeleteVolume makes on its success paths (see
// controllerserver.go), so exercising it here after a real sweep populated the cache proves the
// series is gone immediately, and that an unrelated volume's series is untouched.
func TestDeleteVolumeForgetsHealthMetricsImmediately(t *testing.T) {
	stayingPV := healthReconcilerTestPV("pv-staying", "weka/v2/default", "wekafs-sc", healthReconcilerSecretRef())
	deletedPV := healthReconcilerTestPV("pv-deleted", "weka/v2/doomed", "wekafs-sc", healthReconcilerSecretRef())

	cs, cache, _ := newHealthReconcilerTestServer(t, healthReconcilerSecret(), stayingPV, deletedPV)
	cs.conditionCache = cache
	reconciler := newVolumeHealthReconciler(cs, cache)
	reconciler.reconcileOnce(context.Background())

	before := testutil.CollectAndCount(controllerMetrics.VolumeHealth.Status)
	if before == 0 {
		t.Fatal("expected the sweep to have produced at least one volume-health series")
	}
	stayingBefore := statusFor(t, cache, "weka/v2/default")

	cs.forgetVolumeHealthMetrics(context.Background(), "weka/v2/doomed")

	after := testutil.CollectAndCount(controllerMetrics.VolumeHealth.Status)
	if after != before-1 {
		t.Fatalf("expected exactly one series removed (from %d to %d), got %d remaining", before, before-1, after)
	}
	if _, ok := cache.lookup("weka/v2/doomed"); ok {
		t.Fatal("expected the deleted volume's cache entry to be gone")
	}
	if got := statusFor(t, cache, "weka/v2/default"); got != stayingBefore {
		t.Fatalf("expected the staying volume's series to be untouched, got %v want %v", got, stayingBefore)
	}
}

// TestDeleteVolumeForgetsHealthMetricsIdempotently covers the "already gone" DeleteVolume path,
// which calls forgetVolumeHealthMetrics too: deleting an already-absent volume must still remove its
// series if one exists, must not error or panic, and calling it a second time (as a retried
// DeleteVolume would) must be a harmless no-op.
func TestDeleteVolumeForgetsHealthMetricsIdempotently(t *testing.T) {
	cs, cache, _ := newHealthReconcilerTestServer(t)
	cs.conditionCache = cache

	const handle = "weka/v2/already-gone"
	labels := healthMetricLabels(handle)
	cache.store(handle, volumeConditionEntry{known: true, probedAt: time.Now(), labels: labels})
	controllerMetrics.VolumeHealth.Status.WithLabelValues(labels...).Set(volumeHealthStatusHealthy)

	before := testutil.CollectAndCount(controllerMetrics.VolumeHealth.Status)

	cs.forgetVolumeHealthMetrics(context.Background(), handle)
	if after := testutil.CollectAndCount(controllerMetrics.VolumeHealth.Status); after != before-1 {
		t.Fatalf("expected the series to be removed (from %d to %d), got %d", before, before-1, after)
	}
	if _, ok := cache.lookup(handle); ok {
		t.Fatal("expected the cache entry to be gone")
	}

	// Second call: nothing left to remove, must not error, panic, or change the series count.
	afterFirst := testutil.CollectAndCount(controllerMetrics.VolumeHealth.Status)
	cs.forgetVolumeHealthMetrics(context.Background(), handle)
	if got := testutil.CollectAndCount(controllerMetrics.VolumeHealth.Status); got != afterFirst {
		t.Fatalf("expected the repeat call to be a no-op, series count changed from %d to %d", afterFirst, got)
	}
}

// TestDeleteVolumeForgetsHealthMetricsNilCache is the crash-risk test: DeleteVolume runs on every
// successful delete regardless of whether volume health support is advertised, so conditionCache is
// nil whenever it isn't (also true in dev mode, or when the k8s manager is nil - see
// NewControllerServer). forgetVolumeHealthMetrics must guard that case rather than deref a nil
// *volumeConditionCache's embedded mutex.
func TestDeleteVolumeForgetsHealthMetricsNilCache(t *testing.T) {
	cs := NewControllerServer("test-node", nil, nil, &DriverConfig{}, nil)
	if cs.conditionCache != nil {
		t.Fatal("test setup invalid: expected a nil conditionCache when no manager is supplied")
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("forgetVolumeHealthMetrics panicked with a nil conditionCache: %v", r)
		}
	}()
	cs.forgetVolumeHealthMetrics(context.Background(), "weka/v2/whatever")
}

// TestDeleteVolumeForgetsHealthMetricsUnknownHandle covers a handle that was never in the cache at
// all (e.g. a volume whose health was never probed before it was deleted): removal must be a
// harmless no-op, not an error or a spurious metric mutation.
func TestDeleteVolumeForgetsHealthMetricsUnknownHandle(t *testing.T) {
	cs, cache, _ := newHealthReconcilerTestServer(t)
	cs.conditionCache = cache

	before := testutil.CollectAndCount(controllerMetrics.VolumeHealth.Status)
	cs.forgetVolumeHealthMetrics(context.Background(), "weka/v2/never-seen")
	if got := testutil.CollectAndCount(controllerMetrics.VolumeHealth.Status); got != before {
		t.Fatalf("expected no series change for an unknown handle, before=%d got=%d", before, got)
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
