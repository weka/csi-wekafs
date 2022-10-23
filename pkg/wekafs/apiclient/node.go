package apiclient

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"k8s.io/helm/pkg/urlutil"
)

const (
	NodeRoleBackend    = "COMPUTE"
	NodeRoleFrontend   = "FRONTEND"
	NodeRoleDrive      = "DRIVES"
	NodeRoleManagement = "MANAGEMENT"
)

type WekaNode struct {
	Id          string    `json:"id"`
	NetworkMode string    `json:"network_mode"`
	Mode        string    `json:"mode"`
	Uid         uuid.UUID `json:"uid"`
	Hostname    string    `json:"hostname"`
	Ips         []string  `json:"ips"`
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

func (n *WekaNode) GetBasePath() string {
	return "nodes"
}

func (n *WekaNode) GetApiUrl() string {
	url, err := urlutil.URLJoin(n.GetBasePath(), n.Uid.String())
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

func (a *ApiClient) GetNodes(ctx context.Context, nodes *[]WekaNode) error {
	node := &WekaNode{}

	err := a.Get(ctx, node.GetBasePath(), nil, nodes)
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
	err := a.Get(ctx, n.GetApiUrl(), nil, node)
	if err != nil {
		return err
	}
	return nil
}
