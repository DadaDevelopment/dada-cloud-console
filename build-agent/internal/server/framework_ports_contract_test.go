package server

import (
	"encoding/json"
	"os"
	"testing"
)

// frameworkPortsJSONPath points at the canonical framework -> default port
// table shared by every module that has to guess an app's port. build-agent
// is the module the values are grounded on (the start command it actually
// generates), so this test is the guard against the in-code table drifting
// away from the file that other modules copy it from.
const frameworkPortsJSONPath = "../../../config/platform/framework-ports.json"

type frameworkPortsFile struct {
	FallbackPort int            `json:"_fallback_port"`
	Ports        map[string]int `json:"ports"`
}

func loadFrameworkPortsJSON(t *testing.T) frameworkPortsFile {
	t.Helper()
	raw, err := os.ReadFile(frameworkPortsJSONPath)
	if err != nil {
		t.Fatalf("read %s: %v", frameworkPortsJSONPath, err)
	}
	var f frameworkPortsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse %s: %v", frameworkPortsJSONPath, err)
	}
	return f
}

// TestFrameworkDefaultPortMatchesCanonicalFile fails red on any divergence
// between frameworkDefaultPort and config/platform/framework-ports.json,
// in either direction: a framework known to one side and not the other, or
// a framework both sides know with different ports.
func TestFrameworkDefaultPortMatchesCanonicalFile(t *testing.T) {
	canonical := loadFrameworkPortsJSON(t)

	for framework, wantPort := range canonical.Ports {
		gotPort := frameworkDefaultPort(framework)
		if gotPort == nil {
			t.Errorf("framework %q: canonical file has port %d, frameworkDefaultPort returns nil", framework, wantPort)
			continue
		}
		if *gotPort != wantPort {
			t.Errorf("framework %q: canonical file has port %d, frameworkDefaultPort returns %d", framework, wantPort, *gotPort)
		}
	}

	for _, framework := range knownFrameworksForPortTest {
		if _, ok := canonical.Ports[framework]; !ok {
			t.Errorf("framework %q: frameworkDefaultPort knows this framework, canonical file does not", framework)
		}
	}
}

// knownFrameworksForPortTest enumerates every framework key frameworkDefaultPort
// switches on. Keep this list in lockstep with that switch: it is what lets
// the test above catch a framework added to the code but never added to the
// canonical file.
var knownFrameworksForPortTest = []string{
	"nextjs", "nuxt", "sveltekit", "remix", "react", "nestjs", "node", "express", "fastify", "javascript", "web",
	"vite",
	"fastapi", "django", "python",
	"flask",
	"streamlit",
	"spring", "spring-maven", "spring-gradle", "maven", "gradle", "scala", "sbt", "go", "dockerfile",
	"static",
}
