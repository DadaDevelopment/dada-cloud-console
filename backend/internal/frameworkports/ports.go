// Package frameworkports is the backend module's copy of the canonical
// framework -> default listen port table defined in
// config/platform/framework-ports.json. The three modules that have to
// guess an app's port (backend, build-agent, gitops-agent) each keep their
// own in-code table plus a contract test that fails red the moment their
// table drifts from the JSON file, so this map must be kept byte-for-byte
// in sync with that file's "ports" object.
package frameworkports

import "strings"

// FallbackPort is used when a framework label has no entry in Ports, or is
// empty. Mirrors _fallback_port in config/platform/framework-ports.json.
const FallbackPort = 8080

// Ports maps a lower-case framework label to the port its process listens
// on by default, grounded on the start command build-agent actually
// generates for that framework.
var Ports = map[string]int{
	"nextjs":        3000,
	"nuxt":          3000,
	"sveltekit":     3000,
	"remix":         3000,
	"react":         3000,
	"nestjs":        3000,
	"express":       3000,
	"fastify":       3000,
	"node":          3000,
	"javascript":    3000,
	"web":           3000,
	"vite":          4173,
	"fastapi":       8000,
	"django":        8000,
	"python":        8000,
	"flask":         5000,
	"streamlit":     8501,
	"spring":        8080,
	"spring-maven":  8080,
	"spring-gradle": 8080,
	"maven":         8080,
	"gradle":        8080,
	"scala":         8080,
	"sbt":           8080,
	"go":            8080,
	"dockerfile":    8080,
	"static":        80,
}

// Lookup returns the default port for framework, matched case-insensitively,
// falling back to FallbackPort when the framework is unrecognized or empty.
func Lookup(framework string) int {
	if port, ok := Ports[strings.ToLower(framework)]; ok {
		return port
	}
	return FallbackPort
}
