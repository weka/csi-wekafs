package apiclient

import (
	"fmt"
	"hash/fnv"
)

type KmsVaultCredentials struct {
	KeyIdentifier string
	Namespace     string
	RoleId        string
	SecretId      string
	Url           string
}

// InsecureString returns a string representation of the KmsVaultCredentials. Since very unsafe to print the values even by mistake, we do not call it String()
func (k *KmsVaultCredentials) InsecureString() string {
	if k == nil {
		return ""
	}
	return fmt.Sprintf("KmsVaultCredentials(KeyIdentifier: %s, Namespace: %s, RoleId: %s, SecretId: %s, Url: %s)",
		k.KeyIdentifier, k.Namespace, k.RoleId, k.SecretId, k.Url)
}

type Credentials struct {
	Username                                     string
	Password                                     string
	Organization                                 string
	HttpScheme                                   string
	Endpoints                                    []string
	LocalContainerName                           string
	AutoUpdateEndpoints                          bool
	CaCertificate                                string
	NfsTargetIPs                                 []string
	KmsPreexistingCredentialsForVolumeEncryption KmsVaultCredentials // those are used as is to pass to filesystem creation
	KmsKeyManagementCredentials                  KmsVaultCredentials // those are used by the CSI plugin to connect to vault and create new credentials //TODO: not implemented
}

func (c *Credentials) String() string {
	return fmt.Sprintf("%s://%s:%s@%s",
		c.HttpScheme, c.Username, c.Organization, c.Endpoints)
}

func (c *Credentials) Hash() uint32 {
	h := fnv.New32a()
	s := fmt.Sprintln(
		c.Username,
		c.Password,
		c.Organization,
		c.Endpoints,
		c.NfsTargetIPs,
		c.LocalContainerName,
		c.CaCertificate,
		c.KmsPreexistingCredentialsForVolumeEncryption.InsecureString(),
		c.KmsKeyManagementCredentials.InsecureString(),
	)
	h.Write([]byte(s))
	return h.Sum32()
}
