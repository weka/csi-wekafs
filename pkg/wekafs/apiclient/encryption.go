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
	if !kms.IsSupportedForCommonEncryptionKey() {
		return false
	}

	return true
}

func (a *ApiClient) IsEncryptionEnabledWithKeyPerFilesystem(ctx context.Context) bool {
	if !a.SupportsEncryptionWithKeyPerFilesystem() {
		return false
	}
	kms, err := a.GetKmsConfiguration(ctx)
	if err != nil {
		return false
	}
	if kms == nil {
		return false
	}
	if !kms.IsSupportedForCommonEncryptionKey() {
		return false
	}
	if !kms.IsSupportedForEncryptionKeyPerFilesystem() {
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
