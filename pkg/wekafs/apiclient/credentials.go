package apiclient

import "fmt"

type Credentials struct {
	Username            string
	Password            string
	Organization        string
	HttpScheme          string
	Endpoints           []string
	LocalContainerName  string
	AutoUpdateEndpoints bool
	CaCertificate       string
	NfsTargetIPs        []string
}

func (c *Credentials) String() string {
	return fmt.Sprintf("%s://%s:%s@%s",
		c.HttpScheme, c.Username, c.Organization, c.Endpoints)
}
