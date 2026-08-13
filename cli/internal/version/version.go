// Package version holds the ddc CLI build version, embedded in the
// X-Dada-Client header sent with every API request.
package version

// Version is the ddc CLI release. Bump this on every behavior change that
// touches the wire protocol (headers, auth flow, upload contract) so the
// audit trail on the server can tell releases apart.
//
// Overridable at build time via:
//
//	go build -ldflags "-X github.com/dada-tuda/console/cli/internal/version.Version=1.2.3"
var Version = "0.1.0"
