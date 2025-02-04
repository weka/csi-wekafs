package apiclient

func (a *ApiClient) IsEncryptionEnabled() bool {
	if !a.SupportsEncryptionWithNoKms() {
		return false
	}
	return true // TODO: implement the rest of the function to actually fetch the data
}
