package apiclient

const (
	ApiHttpTimeOutSeconds   = 60
	ApiRetryIntervalSeconds = 1
	ApiRetryMaxCount        = 5
	// ApiMaxPagesPerRequest bounds a paginated fetch, so a backend that keeps returning a next
	// token cannot spin forever.
	ApiMaxPagesPerRequest         = 1000
	RetryBackoffExponentialFactor = 1
	// RetryBackoffTooManyTasksFactor grows the wait between attempts when the cluster reports its
	// task queue is full. Ordinary transient errors keep the flat factor above, which retries
	// quickly because the condition usually clears at once; a full task queue clears only as tasks
	// finish, which takes seconds to minutes, so retrying at the same interval mostly wastes
	// attempts and adds load.
	RetryBackoffTooManyTasksFactor = 2
	// MaxRetryBackoffTooManyTasksSeconds caps a single wait so the doubling above cannot push one
	// attempt past the CSI sidecar operation timeout. With ApiRetryMaxCount attempts the total stays
	// well inside it, so the operation still fails in a bounded time rather than hanging.
	MaxRetryBackoffTooManyTasksSeconds             = 16
	RootOrganizationName                           = "Root"
	TracerName                                     = "weka-csi"
	ApiUserRoleClusterAdmin            ApiUserRole = "ClusterAdmin"
	ApiUserRoleOrgAdmin                ApiUserRole = "OrgAdmin"
	ApiUserRoleTenantAdmin             ApiUserRole = "TenantAdmin"
	ApiUserRoleReadOnly                ApiUserRole = "ReadOnly"
	ApiUserRoleCSI                     ApiUserRole = "CSI"
	ApiUserRoleS3                      ApiUserRole = "S3"
	ApiUserRoleRegular                 ApiUserRole = "Regular"
)
