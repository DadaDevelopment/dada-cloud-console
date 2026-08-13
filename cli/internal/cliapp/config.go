// Package cliapp wires together auth, apiclient and archive into the ddc
// subcommands (login, deploy).
package cliapp

import "os"

// Production defaults. defaultIssuer and defaultAPIBase come from
// helm/dada-cloud-console/values.yaml (KEYCLOAK_ISSUER and the console's own
// origin + "/api/v1"). defaultClientID is "ddc-cli", the public Keycloak
// client provisioned specifically for this CLI's device authorization grant
// (argo-infra console-migration commit 7247486f) - NOT "dada-console", the
// web console's own client, which has the device grant disabled and answers
// unauthorized_client.
const (
	defaultIssuer   = "https://id.dada-tuda.ru/realms/master"
	defaultAPIBase  = "https://console.dada-tuda.ru/api/v1"
	defaultClientID = "ddc-cli"
)

// Config holds the endpoints and client id ddc talks to, resolved from
// environment variable overrides with hardcoded production defaults.
type Config struct {
	Issuer   string
	APIBase  string
	ClientID string
}

// LoadConfig reads DDC_ISSUER, DDC_API_BASE and DDC_CLIENT_ID, falling back
// to the production defaults for anything unset.
func LoadConfig() Config {
	return Config{
		Issuer:   envOr("DDC_ISSUER", defaultIssuer),
		APIBase:  envOr("DDC_API_BASE", defaultAPIBase),
		ClientID: envOr("DDC_CLIENT_ID", defaultClientID),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
