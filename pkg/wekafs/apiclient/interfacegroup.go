package apiclient

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/util/rand"
	"net/url"
	"os"
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
	return []string{"Name", "Gateway", "SubnetMask", "Type"}
}

func (i *InterfaceGroup) GetType() string {
	return "interfaceGroup"
}

//goland:noinspection GoUnusedParameter
func (i *InterfaceGroup) GetBasePath(client *ApiClient) string {
	return "interfaceGroups"
}

func (i *InterfaceGroup) GetApiUrl(client *ApiClient) string {
	url, err := url.JoinPath(i.GetBasePath(client), i.Uid.String())
	if err == nil {
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
func (i *InterfaceGroup) GetIpAddress(ctx context.Context) (string, error) {
	logger := log.Ctx(ctx)
	if i == nil {
		return "", errors.New("interface group is nil")
	}
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
	idx := hashString(hostname, len(i.Ips))
	logger.Debug().Int("index", idx).Str("hostname", hostname).Int("ips", len(i.Ips)).Msg("Selected IP address based on hostname")
	return i.Ips[idx], nil
}

func (i *InterfaceGroup) GetRandomIpAddress(ctx context.Context) (string, error) {
	logger := log.Ctx(ctx)
	if i == nil {
		return "", errors.New("interface group is nil")
	}
	if len(i.Ips) == 0 {
		return "", errors.New("no IP addresses found for interface group")
	}
	idx := rand.Intn(len(i.Ips))
	ip := i.Ips[idx]
	logger.Debug().Str("ip", ip).Msg("Selected random IP address")
	return ip, nil
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

func (a *ApiClient) fetchNfsInterfaceGroup(ctx context.Context, name string) error {
	igs := &[]InterfaceGroup{}
	err := a.GetInterfaceGroupsByType(ctx, InterfaceGroupTypeNFS, igs)
	if err != nil {
		return errors.Join(errors.New("failed to fetch nfs interface groups"), err)
	}
	if len(*igs) == 0 {
		return errors.New("no nfs interface groups found")
	}
	igname := name
	if name == "" {
		igname = "default"
	}
	for _, ig := range *igs {
		if ig.Name == name || name == "" {
			if len(ig.Ips) == 0 {
				if name == "" {
					continue
				}
				return errors.New("no IP addresses found for nfs interface group \"" + name + "\"")
			}
			a.NfsInterfaceGroups[igname] = &ig
			return nil
		}
	}
	return errors.New(fmt.Sprintf("no nfs interface group named '%s' found", name))
}

func (a *ApiClient) GetNfsInterfaceGroup(ctx context.Context, name string) *InterfaceGroup {
	igName := name
	if name == "" {
		igName = "default"
	}
	_, ok := a.NfsInterfaceGroups[igName]
	if !ok {
		err := a.fetchNfsInterfaceGroup(ctx, name)
		if err != nil {
			return nil
		}
	}
	return a.NfsInterfaceGroups[igName]
}

// GetNfsMountIp returns the IP address of the NFS interface group to be used for NFS mount
// TODO: need to do it much more sophisticated way to distribute load
func (a *ApiClient) GetNfsMountIp(ctx context.Context) (string, error) {
	// if override is set, use it
	if len(a.Credentials.NfsTargetIPs) > 0 && a.Credentials.NfsTargetIPs[0] != "" {
		ips := a.Credentials.NfsTargetIPs
		idx := rand.Intn(len(ips))
		ip := ips[idx]
		return ip, nil
	}

	ig := a.GetNfsInterfaceGroup(ctx, a.NfsInterfaceGroupName)
	if ig == nil {
		return "", errors.New("no NFS interface group found")
	}
	if ig.Ips == nil || len(ig.Ips) == 0 {
		return "", errors.New("no IP addresses found for NFS interface group")
	}

	return ig.GetRandomIpAddress(ctx)
}
