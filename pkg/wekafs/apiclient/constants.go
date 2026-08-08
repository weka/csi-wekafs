package apiclient

const (
	ApiHttpTimeOutSeconds   = 60
	ApiRetryIntervalSeconds = 1
	ApiRetryMaxCount        = 5
	// ApiMaxPagesPerRequest bounds a paginated fetch, so a backend that keeps returning a next
	// token cannot spin forever.
	ApiMaxPagesPerRequest                     = 1000
	RetryBackoffExponentialFactor             = 1
	RootOrganizationName                      = "Root"
	TracerName                                = "weka-csi"
	ApiUserRoleClusterAdmin       ApiUserRole = "ClusterAdmin"
	ApiUserRoleOrgAdmin           ApiUserRole = "OrgAdmin"
	ApiUserRoleReadOnly           ApiUserRole = "ReadOnly"
	ApiUserRoleCSI                ApiUserRole = "CSI"
	ApiUserRoleS3                 ApiUserRole = "S3"
	ApiUserRoleRegular            ApiUserRole = "Regular"
)
