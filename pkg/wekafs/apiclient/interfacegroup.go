package apiclient

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"k8s.io/helm/pkg/urlutil"
	"os"
	"sort"
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

func (i *InterfaceGroup) GetBasePath(client *ApiClient) string {
	return "interfaceGroups"
}

func (i *InterfaceGroup) GetApiUrl(client *ApiClient) string {
	url, err := urlutil.URLJoin(i.GetBasePath(client), i.Uid.String())
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

func (i *InterfaceGroup) isSmb() bool {
	return i.getInterfaceGroupType() == InterfaceGroupTypeSMB
}

// GetIpAddress returns a single IP address based on hostname, so for same server, always same IP address will be returned
// This is useful for NFS mount, where we need to have same IP address for same server
// TODO: this could be further optimized in future to avoid a situation where multiple servers are not evenly distributed
// and some IPs are getting more load than others. Could be done, for example, by random selection of IP address etc.
func (i *InterfaceGroup) GetIpAddress() (string, error) {
	if len(i.Ips) == 0 {
		return "", errors.New("no IP addresses found for interface group")
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}
	if hostname == "" {
		hostname = "localhost"
	}

	return i.Ips[hashString(hostname, len(i.Ips))], nil
}

func (a *ApiClient) GetInterfaceGroups(ctx context.Context, interfaceGroups *[]InterfaceGroup) error {
	ig := &InterfaceGroup{}

	err := a.Get(ctx, ig.GetBasePath(a), nil, interfaceGroups)
	if err != nil {
		return err
	}
	return nil
}

func (a *ApiClient) GetInterfaceGroupsByType(ctx context.Context, groupType InterfaceGroupType, interfaceGroups *[]InterfaceGroup) error {
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
	err := a.Get(ctx, ig.GetApiUrl(a), nil, interfaceGroup)
	if err != nil {
		return err
	}
	return nil
}

func (a *ApiClient) fetchNfsInterfaceGroup(ctx context.Context, name *string, useDefault bool) error {
	igs := &[]InterfaceGroup{}
	err := a.GetInterfaceGroupsByType(ctx, InterfaceGroupTypeNFS, igs)
	if err != nil {
		return errors.Join(errors.New("failed to fetch nfs interface groups"), err)
	}
	if len(*igs) == 0 {
		return errors.New("no nfs interface groups found")
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
	if len(a.NfsInterfaceGroup.Ips) == 0 {
		return errors.New("no IP addresses found for nfs interface group")
	}
	// Make sure the IPs are always sorted
	sort.Strings(a.NfsInterfaceGroup.Ips)
	return nil
}

func (a *ApiClient) GetNfsInterfaceGroup(ctx context.Context) *InterfaceGroup {
	if a.NfsInterfaceGroup == nil {
		_ = a.fetchNfsInterfaceGroup(ctx, nil, true)
	}
	return a.NfsInterfaceGroup
}

// GetNfsMountIp returns the IP address of the NFS interface group to be used for NFS mount
// TODO: need to do it much more sophisticated way to distribute load
func (a *ApiClient) GetNfsMountIp(ctx context.Context) (string, error) {
	ig := a.GetNfsInterfaceGroup(ctx)
	if ig == nil {
		return "", errors.New("no NFS interface group found")
	}
	return ig.GetIpAddress()
}
