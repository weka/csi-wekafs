package apiclient

import "testing"

// TestHasCSIPermissions pins which WEKA API user roles the driver will operate as. Getting this
// wrong is not a subtle failure: ensureSufficientPermissions refuses to bring the client up at all,
// so every volume operation fails at login with "does not have sufficient permissions".
func TestHasCSIPermissions(t *testing.T) {
	for _, tc := range []struct {
		role    ApiUserRole
		allowed bool
		why     string
	}{
		{ApiUserRoleCSI, true, "the purpose-built role"},
		{ApiUserRoleClusterAdmin, true, "administers the whole cluster"},
		{ApiUserRoleOrgAdmin, true, "administers one organization"},
		{ApiUserRoleTenantAdmin, true, "same scope as OrgAdmin, one organization"},
		{ApiUserRoleReadOnly, false, "cannot create filesystems, quotas or snapshots"},
		{ApiUserRoleS3, false, "scoped to S3, not filesystem management"},
		{ApiUserRoleRegular, false, "no administrative rights"},
		{"", false, "role could not be determined"},
		{"SomethingWekaAddedLater", false, "unknown roles must not be assumed sufficient"},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			a := &ApiClient{ApiUserRole: tc.role}
			if got := a.HasCSIPermissions(); got != tc.allowed {
				t.Errorf("HasCSIPermissions() for role %q = %v, want %v (%s)", tc.role, got, tc.allowed, tc.why)
			}
		})
	}
}
