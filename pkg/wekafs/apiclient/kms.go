package apiclient

import (
	"fmt"
	"golang.org/x/net/context"
)

type KmsType string
type KmsAuthMethod string

const (
	KmsTypeLocal          KmsType       = "Local"
	KmsTypeHashicorpVault KmsType       = "HashiCorpVault"
	KmsAuthMethodToken    KmsAuthMethod = "token"
	KmsAuthMethodAppRole  KmsAuthMethod = "RoleId/SecretId"
)

type Kms struct {
	KmsType KmsType   `json:"kms_type"`
	Params  KmsParams `json:"params"`
}

type KmsParams struct {
	MasterKeyName string        `json:"master_key_name"`
	BaseUrl       string        `json:"base_url"`
	AuthMethod    KmsAuthMethod `json:"auth_method"`
}

func (k *Kms) GetType() string {
	return "kms"
}

func (k *Kms) GetBasePath(a *ApiClient) string {
	return "/kms"
}

func (k *Kms) GetApiUrl(a *ApiClient) string {
	return k.GetBasePath(a)
}

func (k *Kms) EQ(other ApiObject) bool {
	return true
}

func (k *Kms) getImmutableFields() []string {
	return []string{
		"KmsType", "Params",
	}
}

func (k *Kms) String() string {
	return fmt.Sprintln("Kms(type:", k.KmsType, "URL:", k.Params.BaseUrl, "AuthMetod:", k.Params.AuthMethod, ")")
}

func (k *Kms) IsLocal() bool {
	return k.KmsType == KmsTypeLocal
}

func (k *Kms) IsHashicorpVault() bool {
	return k.KmsType == KmsTypeHashicorpVault
}

func (k *Kms) SupportsPerFilesystemEncryptionKey() bool {
	return k.Params.AuthMethod == KmsAuthMethodAppRole
}

func (k *Kms) IsSupported() bool {
	return k.IsHashicorpVault()
}

func (a *ApiClient) getKms(ctx context.Context) (*Kms, error) {
	responseData := &Kms{}
	err := a.Get(ctx, responseData.GetBasePath(a), nil, responseData)
	if err != nil {
		return nil, err
	}
	return responseData, nil
}

func (a *ApiClient) GetKmsConfiguration(ctx context.Context) (*Kms, error) {
	kms, err := a.getKms(ctx)
	if err != nil {
		return nil, err
	}
	if kms.IsSupported() {
		return kms, nil
	}
	return nil, fmt.Errorf("KMS configuration is not supported")
}

func (a *ApiClient) HasKmsConfiguration(ctx context.Context) bool {
	kms, err := a.GetKmsConfiguration(ctx)
	if err != nil {
		return false
	}
	if kms.IsSupported() {
		return true
	}
	return false
}
