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
	NodeModeBackend    = "backend"
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

func (n *WekaNode) SupportsPagination() bool {
	return false
}

func (n *WekaNode) CombinePartialResponse(next ApiObjectResponse) error {
	panic("implement me")
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
		if a.CompatibilityMap.NewNodeApiObjectPath {
			return "processes"
		}
	}
	return "nodes"
}

func (n *WekaNode) GetApiUrl(a *ApiClient) string {
	url, err := urlutil.URLJoin(n.GetBasePath(a), n.Uid.String())
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

type WekaNodes []*WekaNode

func (w *WekaNodes) SupportsPagination() bool {
	return true
}

func (w *WekaNodes) CombinePartialResponse(next ApiObjectResponse) error {
	if partialList, ok := next.(*WekaNodes); ok {
		*w = append(*w, *partialList...)
		return nil
	}
	return fmt.Errorf("invalid partial response")
}

func (a *ApiClient) GetNodes(ctx context.Context, nodes *WekaNodes) error {
	node := &WekaNode{}

	err := a.Get(ctx, node.GetBasePath(a), nil, nodes)
	if err != nil {
		return err
	}
	return nil
}

func (a *ApiClient) GetNodesByRole(ctx context.Context, role string, nodes *WekaNodes) error {
	res := &WekaNodes{}
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
