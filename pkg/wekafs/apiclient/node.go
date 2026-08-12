package apiclient

import (
	"context"
	"fmt"
	"net/url"

	"github.com/google/uuid"
)

const (
	NodeRoleBackend    = "COMPUTE"
	NodeRoleFrontend   = "FRONTEND"
	NodeRoleDrive      = "DRIVES"
	NodeRoleManagement = "MANAGEMENT"
	// NodeRoleDataServices is carried by the process running inside a data services container. That
	// container is what runs QUOTA_COLORING: setting a quota on a directory that already holds data
	// has to walk the whole tree stamping the quota ID onto every file, and without this the walk
	// runs inline in a single process.
	//
	// This role is the ONLY way the WEKA API distinguishes such a container. The containers endpoint
	// reports it as an ordinary "backend" - verified on WEKA 5.1.24, where adding one moved the
	// backend count from 12 to 13 and added no new mode value.
	NodeRoleDataServices = "DATASERV"
	NodeModeBackend      = "backend"
)

type WekaNode struct {
	Id          string    `json:"id"`
	NetworkMode string    `json:"network_mode"`
	Mode        string    `json:"mode"`
	Uid         uuid.UUID `json:"uid"`
	Hostname    string    `json:"hostname"`
	Ips         []string  `json:"ips"`
	MgmtPort    int       `json:"mgmt_port,omitempty"`
	Slot        int       `json:"slot"`
	Roles       []string  `json:"roles"`
	Status      string    `json:"status"`
}

func (n *WekaNode) String() string {
	return fmt.Sprintln("WekaNode Id:", n.Id, "roles:", n.Roles)
}

func (n *WekaNode) getImmutableFields() []string {
	return []string{"Id", "Uid", "Slot"}
}

func (n *WekaNode) GetType() string {
	return "wekanode"
}

func (n *WekaNode) GetBasePath(a *ApiClient) string {
	if a != nil {
		if a.compatibility().NewNodeApiObjectPath {
			return "processes"
		}
	}
	return "nodes"
}

func (n *WekaNode) GetApiUrl(a *ApiClient) string {
	url, err := url.JoinPath(n.GetBasePath(a), n.Uid.String())
	if err != nil {
		return url
	}
	return ""
}

func (n *WekaNode) EQ(other ApiObject) bool {
	return ObjectsAreEqual(n, other)
}

func (n *WekaNode) hasRole(role string) bool {
	for _, r := range n.Roles {
		if r == role {
			return true
		}
	}
	return false
}

func (n *WekaNode) isBackend() bool {
	return n.hasRole(NodeRoleBackend)
}
func (n *WekaNode) isFrontend() bool {
	return n.hasRole(NodeRoleFrontend)
}
func (n *WekaNode) isMgmt() bool {
	return n.hasRole(NodeRoleManagement)
}
func (n *WekaNode) isDrive() bool {
	return n.hasRole(NodeRoleDrive)
}

func (n *WekaNode) isDataServices() bool {
	return n.hasRole(NodeRoleDataServices)
}

func (a *ApiClient) GetNodes(ctx context.Context, nodes *[]WekaNode) error {
	node := &WekaNode{}

	err := a.Get(ctx, node.GetBasePath(a), nil, nodes)
	if err != nil {
		return err
	}
	return nil
}

func (a *ApiClient) GetNodesByRole(ctx context.Context, role string, nodes *[]WekaNode) error {
	res := &[]WekaNode{}
	err := a.GetNodes(ctx, res)
	if err != nil {
		return nil
	}
	for _, n := range *res {
		if n.hasRole(role) {
			*nodes = append(*nodes, n)
		}
	}
	return nil
}

func (a *ApiClient) GetNodeByUid(ctx context.Context, uid uuid.UUID, node *WekaNode) error {
	n := &WekaNode{
		Uid: uid,
	}
	err := a.Get(ctx, n.GetApiUrl(a), nil, node)
	if err != nil {
		return err
	}
	return nil
}

// HasDataServicesProcess reports whether the cluster runs a data services process, i.e. whether a
// data services container is deployed and up.
//
// Only processes reporting status UP count. A container that exists but is down does not run the
// coloring task, and treating it as present would have the driver hand work to something that will
// never pick it up.
func (a *ApiClient) HasDataServicesProcess(ctx context.Context) (bool, error) {
	// Deliberately not GetNodesByRole: that helper swallows the fetch error and returns an empty
	// list, which here would be indistinguishable from "the cluster has no data services container"
	// - the one confusion this whole capability check exists to avoid.
	nodes := &[]WekaNode{}
	if err := a.GetNodes(ctx, nodes); err != nil {
		return false, err
	}
	for _, n := range *nodes {
		if n.isDataServices() && n.Status == "UP" {
			return true, nil
		}
	}
	return false, nil
}
