package tutk

import (
	"fmt"
	"net/url"
)

// Credentials holds TUTK P2P camera credentials obtained from the Bambu Cloud API
// via the POST /v1/iot-service/api/user/ttcode endpoint.
type Credentials struct {
	TTCode  string // TUTK P2P UID (ttcode)
	AuthKey string // Authentication key
	Passwd  string // 6-character hex password
	Region  string // "us" or "cn"
	Serial  string // Printer serial number (dev_id)

	// Optional URL parameters
	NetVer string // network plugin version, default "01.09.03.01"
	DevVer string // printer firmware version, default ""
	CliID  string // client UUID, auto-generated if empty
	CliVer string // client version, default "1.0.0"
}

// Defaults
const (
	defaultNetVer = "01.09.03.01"
	defaultCliVer = "1.0.0"
)

// BuildURL constructs the bambu:///tutk?... P2P URL from credentials.
// The URL format is used by libBambuSource.so's Bambu_Create() to establish
// a P2P connection via TUTK relay.
func BuildURL(creds Credentials) string {
	netVer := creds.NetVer
	if netVer == "" {
		netVer = defaultNetVer
	}
	cliVer := creds.CliVer
	if cliVer == "" {
		cliVer = defaultCliVer
	}
	cliID := creds.CliID
	if cliID == "" {
		cliID = "00000000-0000-0000-0000-000000000000" // placeholder
	}
	devVer := creds.DevVer
	if devVer == "" {
		devVer = "00.00.00.00"
	}

	q := url.Values{}
	q.Set("uid", creds.TTCode)
	q.Set("authkey", creds.AuthKey)
	q.Set("passwd", creds.Passwd)
	q.Set("region", creds.Region)
	q.Set("device", creds.Serial)
	q.Set("net_ver", netVer)
	q.Set("dev_ver", devVer)
	q.Set("refresh_url", "1")
	q.Set("cli_id", cliID)
	q.Set("cli_ver", cliVer)

	return fmt.Sprintf("bambu:///tutk?%s", q.Encode())
}
