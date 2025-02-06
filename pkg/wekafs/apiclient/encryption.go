package apiclient

import "context"

func (a *ApiClient) IsEncryptionEnabled(ctx context.Context) bool {
	if !a.SupportsEncryptionWithCommonKey() {
		return false
	}

	kms, err := a.GetKmsConfiguration(ctx)
	if err != nil {
		return false
	}
	if kms == nil {
		return false
	}
	if !kms.IsSupported() {
		return false
	}

	return true
}

type EncryptionParams struct {
	Encrypted             bool
	AllowNoKms            bool
	KmsVaultKeyIdentifier string
	KmsVaultNamespace     string
	KmsVaultRoleId        string
	KmsVaultSecretId      string
}
