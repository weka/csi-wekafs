package apiclient

import (
	"context"
	"flag"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/rand"
	"testing"
)

var creds Credentials
var endpoint string
var fsName string

var client *ApiClient

func TestMain(m *testing.M) {
	flag.StringVar(&endpoint, "api-endpoint", "vm49-1723969301909816-0.lan:14000", "API endpoint for tests")
	flag.StringVar(&creds.Username, "api-username", "admin", "API username for tests")
	flag.StringVar(&creds.Password, "api-password", "AAbb1234", "API password for tests")
	flag.StringVar(&creds.Organization, "api-org", "Root", "API org for tests")
	flag.StringVar(&creds.HttpScheme, "api-scheme", "https", "API scheme for tests")
	flag.StringVar(&fsName, "fs-name", "default", "Filesystem name for tests")
	flag.Parse()
	m.Run()
}

func GetApiClientForTest(t *testing.T) *ApiClient {
	creds.Endpoints = []string{endpoint}
	if client == nil {
		apiClient, err := NewApiClient(context.Background(), creds, true, "test")
		if err != nil {
			t.Fatalf("Failed to create API client: %v", err)
		}
		if apiClient == nil {
			t.Fatalf("Failed to create API client")
		}
		if err := apiClient.Login(context.Background()); err != nil {
			t.Fatalf("Failed to login: %v", err)
		}
		client = apiClient
	}
	return client
}

//
//func TestGetNfsPermissions(t *testing.T) {
//	apiClient := GetApiClientForTest(t)
//
//	var permissions []NfsPermission
//
//	req := &NfsPermissionCreateRequest{
//		Filesystem: fsName,
//		Group:      "group1",
//	}
//	p := &NfsPermission{}
//	err := apiClient.CreateNfsPermission(context.Background(), &NfsPermissionCreateRequest{}, p)
//	assert.NoError(t, err)
//	assert.NotZero(t, p.Uid)
//
//	err := apiClient.GetNfsPermissions(context.Background(), &permissions)
//	assert.NoError(t, err)
//	assert.NotEmpty(t, permissions)
//}
//
//func TestFindNfsPermissionsByFilter(t *testing.T) {
//	apiClient := GetApiClientForTest(t)
//	query := &NfsPermission{Filesystem: "fs1"}
//	var resultSet []NfsPermission
//	err := apiClient.FindNfsPermissionsByFilter(context.Background(), query, &resultSet)
//	assert.NoError(t, err)
//	assert.NotEmpty(t, resultSet)
//}
//
//func TestGetNfsPermissionByFilter(t *testing.T) {
//	apiClient := GetApiClientForTest(t)
//
//	query := &NfsPermission{Filesystem: "fs1"}
//	result, err := apiClient.GetNfsPermissionByFilter(context.Background(), query)
//	assert.NoError(t, err)
//	assert.NotNil(t, result)
//}
//
//func TestGetNfsPermissionsByFilesystemName(t *testing.T) {
//	apiClient := GetApiClientForTest(t)
//
//
//	var permissions []NfsPermission
//	err := apiClient.GetNfsPermissionsByFilesystemName(context.Background(), "fs1", &permissions)
//	assert.NoError(t, err)
//	assert.NotEmpty(t, permissions)
//}
//
//func TestGetNfsPermissionByUid(t *testing.T) {
//	apiClient := GetApiClientForTest(t)
//	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//		w.WriteHeader(http.StatusOK)
//		w.Write([]byte(`{"filesystem": "fs1", "group": "group1"}`))
//	}))
//	defer server.Close()
//
//
//	uid := uuid.New()
//	result, err := apiClient.GetNfsPermissionByUid(context.Background(), uid)
//	assert.NoError(t, err)
//	assert.NotNil(t, result)
//}
//
//func TestCreateNfsPermission(t *testing.T) {
//	apiClient := GetApiClientForTest(t)
//
//	req := &NfsPermissionCreateRequest{
//		Filesystem:      "fs1",
//		Group:           "group1",
//		SquashMode:      NfsPermissionSquashModeNone,
//		AnonUid:         1000,
//		AnonGid:         1000,
//		EnableAuthTypes: []NfsAuthType{NfsAuthTypeSys},
//	}
//	var perm NfsPermission
//	err := apiClient.CreateNfsPermission(context.Background(), req, &perm)
//	assert.NoError(t, err)
//	assert.NotNil(t, perm)
//}
//
//func TestEnsureNfsPermission(t *testing.T) {
//	apiClient := GetApiClientForTest(t)
//	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//		w.WriteHeader(http.StatusOK)
//		w.Write([]byte(`{"filesystem": "fs1", "group": "group1"}`))
//	}))
//	defer server.Close()
//
//
//	err := EnsureNfsPermission(context.Background(), "fs1", "group1", apiClient)
//	assert.NoError(t, err)
//}

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
	result, err := apiClient.EnsureCsiPluginNfsClientGroup(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestNfsClientGroupRules(t *testing.T) {
	apiClient := GetApiClientForTest(t)
	cg, err := apiClient.EnsureCsiPluginNfsClientGroup(context.Background())
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
	err = apiClient.GetNfsClientGroupRules(context.Background(), rules)
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
	err := apiClient.EnsureNfsPermissions(context.Background(), "172.16.5.147", "default")
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
	assert.NotEmpty(t, igs[0].Ips)
	//
	//// Test GetGroupByUid
	//uid := ig.Uid
	//err = apiClient.GetInterfaceGroupByUid(context.Background(), uid, ig)
	//assert.NoError(t, err)
	//assert.Equal(t, igName, ig.Name)
	//assert.NotEmpty(t, ig.Uid)
	//
	//// Test GetGroupByName
	//name := ig.Name
	//ig, err = apiClient.GetInterfaceGroupByName(context.Background(), name)
	//assert.NoError(t, err)
	//assert.Equal(t, igName, ig.Name)
	//assert.NotEmpty(t, ig.Uid)
	//
	//// Test Delete
	//r := &InterfaceGroupDeleteRequest{Uid: ig.Uid}
	//err = apiClient.DeleteInterfaceGroup(context.Background(), r)
	//assert.NoError(t, err)
	//err = apiClient.GetInterfaceGroups(context.Background(), &igs)
	//assert.NoError(t, err)
	//for _, r := range igs {
	//	if r.Uid == ig.Uid {
	//		t.Errorf("Failed to delete group")
	//	}
	//}
}
