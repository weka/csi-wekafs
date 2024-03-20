package apiclient

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"k8s.io/helm/pkg/urlutil"
)

type InterfaceGroupType string

const (
	InterfaceGroupTypeNFS InterfaceGroupType = "NFS"
	InterfaceGroupTypeSMB InterfaceGroupType = "SMB"
)

type InterfaceGroup struct {
	SubnetMask      string             `json:"subnet_mask"`
	Name            string             `json:"name"`
	Uid             uuid.UUID          `json:"uid"`
	Ips             []string           `json:"ips"`
	AllowManageGids bool               `json:"allow_manage_gids"`
	Type            InterfaceGroupType `json:"type"`
	Gateway         string             `json:"gateway"`
	Status          string             `json:"status"`
}

func (i *InterfaceGroup) String() string {
	return fmt.Sprintln("InterfaceGroup ", i.Name, "Uid:", i.Uid.String(), "type:", i.Type, "status:", i.Status)
}

func (i *InterfaceGroup) getImmutableFields() []string {
	return []string{"Id", "Uid", "Slot"}
}

func (i *InterfaceGroup) GetType() string {
	return "interfaceGroup"
}

func (i *InterfaceGroup) GetBasePath() string {
	return "nodes"
}

func (i *InterfaceGroup) GetApiUrl() string {
	url, err := urlutil.URLJoin(i.GetBasePath(), i.Uid.String())
	if err != nil {
		return url
	}
	return ""
}

func (i *InterfaceGroup) EQ(other ApiObject) bool {
	return ObjectsAreEqual(i, other)
}

func (i *InterfaceGroup) getInterfaceGroupType() InterfaceGroupType {
	return i.Type
}

func (i *InterfaceGroup) isNfs() bool {
	return i.getInterfaceGroupType() == InterfaceGroupTypeNFS
}

func (a *ApiClient) GetInterfaceGroups(ctx context.Context, intefaceGroups *[]InterfaceGroup) error {
	ig := &InterfaceGroup{}

	err := a.Get(ctx, ig.GetBasePath(), nil, intefaceGroups)
	if err != nil {
		return err
	}
	return nil
}

func (a *ApiClient) GetIntefaceGroupsByType(ctx context.Context, groupType InterfaceGroupType, interfaceGroups *[]InterfaceGroup) error {
	res := &[]InterfaceGroup{}
	err := a.GetInterfaceGroups(ctx, res)
	if err != nil {
		return nil
	}
	for _, ig := range *res {
		if ig.getInterfaceGroupType() == groupType {
			*interfaceGroups = append(*interfaceGroups, ig)
		}
	}
	return nil
}

func (a *ApiClient) GetInterfaceGroupByUid(ctx context.Context, uid uuid.UUID, interfaceGroup *InterfaceGroup) error {
	ig := &InterfaceGroup{
		Uid: uid,
	}
	err := a.Get(ctx, ig.GetApiUrl(), nil, interfaceGroup)
	if err != nil {
		return err
	}
	return nil
}

func (a *ApiClient) fetchNfsInterfaceGroup(ctx context.Context, name *string, useDefault bool) error {
	igs := &[]InterfaceGroup{}
	err := a.GetIntefaceGroupsByType(ctx, InterfaceGroupTypeNFS, igs)
	if err != nil {
		return errors.Join(errors.New("failed to fetch nfs inteface groups"), err)
	}
	if len(*igs) == 0 {
		return errors.New("no nfs inteface groups found")
	}
	if name != nil {
		for _, ig := range *igs {
			if ig.Name == *name {
				a.NfsInterfaceGroup = &ig
			}
		}
	} else if useDefault {
		a.NfsInterfaceGroup = &(*igs)[0]
	}
	return nil
}

func (a *ApiClient) GetNfsIntefaceGroup(ctx context.Context) *InterfaceGroup {
	if a.NfsInterfaceGroup == nil {
		_ = a.fetchNfsInterfaceGroup(ctx, nil, true)
	}
	return a.NfsInterfaceGroup
}
