package convert

import (
	"encoding/base64"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// RedactionMarker replaces the value of a sensitive Secret key when an export is taken
// without --include-secret-data. It is deliberately not valid base64-decoded credential
// material and is recognisable on sight, so that an operator who applies such an archive by
// hand sees immediately why the driver cannot authenticate.
const RedactionMarker = "REDACTED-BY-WEKA-CSI-MIGRATOR"

// redactionMarkerB64 is what actually lands in a Secret's data map, which holds base64. It
// is derived once so that redacting and detecting redaction can never disagree.
var redactionMarkerB64 = base64.StdEncoding.EncodeToString([]byte(RedactionMarker))

// SensitiveSecretKeys are the keys of a Weka CSI API secret that carry credentials.
//
// Everything else the driver reads — username, organization, endpoints, scheme,
// nfsTargetIps, localContainerName, autoUpdateEndpoints, caCertificate — is left intact.
// Those fields are precisely what an operator must review and edit when moving to a
// different Weka cluster or network segment, so redacting them would make a redacted
// export useless for planning a migration.
//
// Keys are matched exactly against the driver's own lookups in pkg/wekafs/wekafs.go.
var SensitiveSecretKeys = []string{
	"password",
	"kmsVaultRoleIdForFilesystemEncryption",
	"kmsVaultSecretIdForFilesystemEncryption",
}

// RedactSecret replaces sensitive values in a Secret and reports which keys it touched.
// The key names themselves are preserved, so the shape of the secret stays reviewable.
func RedactSecret(u *unstructured.Unstructured) ([]string, error) {
	data, found, err := unstructured.NestedMap(u.Object, "data")
	if err != nil {
		return nil, fmt.Errorf("reading data of secret %s/%s: %w", u.GetNamespace(), u.GetName(), err)
	}
	if !found {
		return nil, nil
	}

	var redacted []string
	for _, key := range SensitiveSecretKeys {
		value, ok := data[key]
		if !ok {
			continue
		}
		// An empty value carries no secret, so redacting it would only add noise.
		if s, isString := value.(string); isString && (s == "" || s == redactionMarkerB64) {
			continue
		}
		data[key] = redactionMarkerB64
		redacted = append(redacted, key)
	}
	if len(redacted) == 0 {
		return nil, nil
	}
	sort.Strings(redacted)
	if err := unstructured.SetNestedMap(u.Object, data, "data"); err != nil {
		return nil, fmt.Errorf("redacting secret %s/%s: %w", u.GetNamespace(), u.GetName(), err)
	}
	return redacted, nil
}

// RedactedKeys reports which sensitive keys of a Secret still hold the redaction marker.
// Import uses it to refuse an archive whose credentials were scrubbed, rather than applying
// secrets that would fail authentication at first mount with a confusing error.
func RedactedKeys(u *unstructured.Unstructured) []string {
	data, found, err := unstructured.NestedMap(u.Object, "data")
	if err != nil || !found {
		return nil
	}
	var redacted []string
	for _, key := range SensitiveSecretKeys {
		if value, ok := data[key].(string); ok && value == redactionMarkerB64 {
			redacted = append(redacted, key)
		}
	}
	sort.Strings(redacted)
	return redacted
}

// NeatSecret strips server-managed metadata from a Secret.
func NeatSecret(u *unstructured.Unstructured) {
	Neat(u)
	// Service account token secrets are populated by the control plane and must never be
	// carried across; the Weka CSI secrets this tool exports are always Opaque.
	unstructured.RemoveNestedField(u.Object, "metadata", "annotations", "kubernetes.io/service-account.uid")
}
