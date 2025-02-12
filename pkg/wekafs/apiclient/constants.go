package apiclient

const (
	ApiHttpTimeOutSeconds                     = 60
	ApiRetryIntervalSeconds                   = 1
	ApiRetryMaxCount                          = 5
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
