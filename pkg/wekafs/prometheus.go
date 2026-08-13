package wekafs

import (
	v1 "k8s.io/api/core/v1"
)

var (
	// LabelsForCsiVolumes identifies one PersistentVolume. It is deliberately specific: the series
	// are per volume by design, and the identifiers are what makes a volume's metrics findable.
	//
	// organization is the Weka tenant a volume belongs to, taken from the credentials its API client
	// authenticated with. Every volume has exactly one, so it adds no series - it only makes the
	// existing ones groupable by tenant.
	//
	// secret_name is the API Secret a volume authenticates with, as "namespace/name". A tenant is
	// expected to use one Secret, so like organization it adds no series to a per-volume metric - it
	// makes API load and capacity attributable to the credentials that produced them. Where that
	// expectation is broken and one tenant spans several Secrets, the tenant's series split.
	//
	// This list is a published contract. Once a release ships metrics carrying it, adding or
	// removing a label breaks every dashboard, recording rule and alert written against them, so it
	// is fixed here in its final shape before the first release that exports any of these series.
	LabelsForCsiVolumes = []string{"csi_driver_name", "pv_name", "cluster_guid", "storage_class_name", "filesystem_name", "volume_type", "organization", "pvc_name", "pvc_namespace", "pvc_uid", "secret_name"}
)

// csiVolumeLabelValues builds the LabelsForCsiVolumes label values for one PersistentVolume, in
// order. It is the one place that convention is spelled out, so every caller that identifies a
// volume for a metric labels it the same way. pvc_name/pvc_namespace/pvc_uid are blank for a PV with
// no claim ref, e.g. one that was provisioned but never bound.
func csiVolumeLabelValues(driverName string, pv *v1.PersistentVolume, clusterGuid, filesystemName, volumeType, organization string) []string {
	labelValues := []string{
		driverName,
		pv.Name,
		clusterGuid,
		pv.Spec.StorageClassName,
		filesystemName,
		volumeType,
		organization,
	}
	if pv.Spec.ClaimRef != nil {
		labelValues = append(labelValues,
			pv.Spec.ClaimRef.Name,
			pv.Spec.ClaimRef.Namespace,
			string(pv.Spec.ClaimRef.UID))
	} else {
		labelValues = append(labelValues, "", "", "")
	}
	// Taken from the PersistentVolume rather than passed in: CSI hands the driver a Secret's
	// contents and never its name, so the object is the only place the name survives. Callers
	// resolve credentials through preferredSecretRef, so labelling with the same ref keeps a
	// volume's series attributable to the Secret that actually authenticated it.
	return append(labelValues, secretRefLabel(pv))
}

// secretRefLabel renders a volume's API Secret as "namespace/name", or blank when the volume
// carries no usable ref - a statically provisioned volume need not reference one at all.
func secretRefLabel(pv *v1.PersistentVolume) string {
	ref := preferredSecretRef(pv)
	if ref == nil {
		return ""
	}
	return ref.Namespace + "/" + ref.Name
}
