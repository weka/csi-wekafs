package apiclient

import (
	"context"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/rand"
	"testing"
)

func TestFindNfsPermissionsByFilesystemName(t *testing.T) {
	apiClient := GetApiClientForTest(t)

	var permissions []NfsPermission
	err := apiClient.FindNfsPermissionsByFilesystem(context.Background(), "snapvolFilesystem", &permissions)
	assert.NoError(t, err)
	assert.NotEmpty(t, permissions)
	if len(permissions) > 0 {
		for _, p := range permissions {
			r := &NfsPermissionDeleteRequest{Uid: p.Uid}
			err := apiClient.DeleteNfsPermission(context.Background(), r)
			assert.NoError(t, err)
		}
	}
	err = apiClient.FindNfsPermissionsByFilesystem(context.Background(), "snapvolFilesystem", &permissions)
	assert.NoError(t, err)
	assert.Empty(t, permissions)

}

func TestNfsClientGroup(t *testing.T) {
	apiClient := GetApiClientForTest(t)

	var clientGroups []NfsClientGroup
	var cg = &NfsClientGroup{
		Uid: uuid.New(),
	}
	// Test GetApiUrl
	assert.NotEmpty(t, cg.GetApiUrl(apiClient))
	assert.Contains(t, cg.GetApiUrl(apiClient), cg.Uid.String())

	// Test EQ
	cg1 := &NfsClientGroup{
		Name: "test",
	}

	cg2 := &NfsClientGroup{
		Name: "test",
	}
	assert.True(t, cg1.EQ(cg2))

	// Test GetBasePath
	assert.NotEmpty(t, cg.GetBasePath(apiClient))

	// Test Create
	cgName := rand.String(10)
	err := apiClient.CreateNfsClientGroup(context.Background(), &NfsClientGroupCreateRequest{Name: cgName}, cg)
	assert.NotEmpty(t, cg.Uid)
	assert.NoError(t, err)
	assert.Equal(t, cgName, cg.Name)
	assert.Empty(t, cg.Rules)

	// Test GetGroups
	err = apiClient.GetNfsClientGroups(context.Background(), &clientGroups)
	assert.NoError(t, err)
	assert.NotEmpty(t, clientGroups)

	// Test GetGroupByUid
	uid := cg.Uid
	err = apiClient.GetNfsClientGroupByUid(context.Background(), uid, cg)
	assert.NoError(t, err)
	assert.Equal(t, cgName, cg.Name)
	assert.NotEmpty(t, cg.Uid)

	// Test GetGroupByName
	name := cg.Name
	cg, err = apiClient.GetNfsClientGroupByName(context.Background(), name)
	assert.NoError(t, err)
	assert.Equal(t, cgName, cg.Name)
	assert.NotEmpty(t, cg.Uid)

	// Test Delete
	r := &NfsClientGroupDeleteRequest{Uid: cg.Uid}
	err = apiClient.DeleteNfsClientGroup(context.Background(), r)
	assert.NoError(t, err)
	err = apiClient.GetNfsClientGroups(context.Background(), &clientGroups)
	assert.NoError(t, err)
	for _, r := range clientGroups {
		if r.Uid == cg.Uid {
			t.Errorf("Failed to delete group")
		}
	}
}

func TestEnsureCsiPluginNfsClientGroup(t *testing.T) {
	apiClient := GetApiClientForTest(t)
	ctx := context.Background()
	result, _, err := apiClient.EnsureCsiPluginNfsClientGroup(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestNfsClientGroupRules(t *testing.T) {
	apiClient := GetApiClientForTest(t)
	ctx := context.Background()
	cg, _, err := apiClient.EnsureCsiPluginNfsClientGroup(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, cg)

	// Test Create
	r := &NfsClientGroupRule{}
	//r2 := &NfsClientGroupRule{}

	req1 := NewNfsClientGroupRuleCreateRequest(cg.Uid, NfsClientGroupRuleTypeIP, "192.168.1.1")
	req2 := NewNfsClientGroupRuleCreateRequest(cg.Uid, NfsClientGroupRuleTypeIP, "192.168.2.0/24")
	req3 := NewNfsClientGroupRuleCreateRequest(cg.Uid, NfsClientGroupRuleTypeIP, "192.168.3.0/255.255.255.255")
	req4 := NewNfsClientGroupRuleCreateRequest(cg.Uid, NfsClientGroupRuleTypeDNS, "test-hostname")
outerLoop:
	for _, req := range []*NfsClientGroupRuleCreateRequest{req1, req2, req3, req4} {
		for _, rule := range cg.Rules {
			if rule.Type == req.Type && rule.Rule == req.AsRule() {
				continue outerLoop
			}
		}
		assert.NotNil(t, req)
		//req2 := &NfsClientGroupRuleCreateRequest{Type: NfsClientGroupRuleTypeDNS, Hostname: "test-hostname", NfsClientGroupUid: cg.Uid}

		err = apiClient.CreateNfsClientGroupRule(context.Background(), req, r)
		assert.NoError(t, err)
	}
	rules := &[]NfsClientGroupRule{}
	err = apiClient.GetNfsClientGroupRules(ctx, rules)
	assert.NoError(t, err)
	assert.NotEmpty(t, rules)
	for _, rule := range *rules {
		assert.NotEmpty(t, rule.Uid)
		assert.NotEmpty(t, rule.Type)
		assert.NotEmpty(t, rule.Rule)
		assert.NotEmpty(t, rule.Id)
	}
}

func TestNfsEnsureNfsPermissions(t *testing.T) {
	apiClient := GetApiClientForTest(t)

	// Test EnsureNfsPermission
	ctx := context.Background()
	err := apiClient.EnsureNfsPermissions(ctx, "default", NfsVersionV4, NfsClientGroupName)
	assert.NoError(t, err)
}

func TestInterfaceGroup(t *testing.T) {
	apiClient := GetApiClientForTest(t)

	var igs []InterfaceGroup
	var ig = &InterfaceGroup{
		Uid: uuid.New(),
	}
	// Test GetApiUrl
	assert.NotEmpty(t, ig.GetApiUrl(apiClient))
	assert.Contains(t, ig.GetApiUrl(apiClient), ig.Uid.String())

	// Test EQ
	ig1 := &InterfaceGroup{
		Name: "test",
	}

	ig2 := &InterfaceGroup{
		Name: "test",
	}
	assert.True(t, ig1.EQ(ig2))

	// Test GetBasePath
	assert.NotEmpty(t, ig.GetBasePath(apiClient))

	// Test Create
	// Test GetGroups
	err := apiClient.GetInterfaceGroups(context.Background(), &igs)
	assert.NoError(t, err)
	assert.NotEmpty(t, igs)
	if len(igs) > 0 {
		assert.NotEmpty(t, igs[0].Ips)
	}
}

func TestIsSupersetOf(t *testing.T) {
	// Test case 1: IP rule superset
	rule1 := &NfsClientGroupRule{
		Type: NfsClientGroupRuleTypeIP,
		Rule: "192.168.1.0/24",
	}
	rule2 := &NfsClientGroupRule{
		Type: NfsClientGroupRuleTypeIP,
		Rule: "192.168.1.1",
	}
	assert.True(t, rule1.IsSupersetOf(rule2))

	// Test case 2: IP rule not superset
	rule3 := &NfsClientGroupRule{
		Type: NfsClientGroupRuleTypeIP,
		Rule: "192.168.2.0/24",
	}
	assert.False(t, rule1.IsSupersetOf(rule3))

	// Test case 3: Non-IP rule
	rule4 := &NfsClientGroupRule{
		Type: NfsClientGroupRuleTypeDNS,
		Rule: "example.com",
	}
	assert.False(t, rule1.IsSupersetOf(rule4))

	// Test case 4: Same rule
	assert.True(t, rule1.IsSupersetOf(rule1))
}
