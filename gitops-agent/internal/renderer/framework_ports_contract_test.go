package renderer

import (
	"encoding/json"
	"os"
	"testing"
)

// canonicalFrameworkPortsFile is the single source of truth every module's
// in-code table must mirror. Backend, build-agent, and gitops-agent each
// keep an independent copy plus this same style of test, so a drift in any
// one module fails only that module's CI.
const canonicalFrameworkPortsFile = "../../../config/platform/framework-ports.json"

type canonicalFrameworkPortsDoc struct {
	FallbackPort int            `json:"_fallback_port"`
	Ports        map[string]int `json:"ports"`
}

func loadCanonicalFrameworkPorts(t *testing.T) canonicalFrameworkPortsDoc {
	t.Helper()
	raw, err := os.ReadFile(canonicalFrameworkPortsFile)
	if err != nil {
		t.Fatalf("read canonical framework-ports.json: %v", err)
	}
	var doc canonicalFrameworkPortsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse canonical framework-ports.json: %v", err)
	}
	return doc
}

// TestDefaultFrameworkPortsMatchCanonicalFile fails red on any divergence
// between defaultFrameworkPorts and config/platform/framework-ports.json, in
// either direction: a framework known to one side and not the other, or a
// framework both sides know with a different port.
func TestDefaultFrameworkPortsMatchCanonicalFile(t *testing.T) {
	doc := loadCanonicalFrameworkPorts(t)

	for framework, wantPort := range doc.Ports {
		gotPort, ok := defaultFrameworkPorts[framework]
		if !ok {
			t.Errorf("defaultFrameworkPorts is missing %q (canonical file has port %d)", framework, wantPort)
			continue
		}
		if gotPort != wantPort {
			t.Errorf("defaultFrameworkPorts[%q] = %d, canonical file has %d", framework, gotPort, wantPort)
		}
	}

	for framework := range defaultFrameworkPorts {
		if _, ok := doc.Ports[framework]; !ok {
			t.Errorf("defaultFrameworkPorts has extra entry %q not present in canonical file", framework)
		}
	}
}

// TestDefaultFrameworkFallbackPortMatchesCanonicalFile guards the fallback
// value the same way: it must equal canonical file's _fallback_port.
func TestDefaultFrameworkFallbackPortMatchesCanonicalFile(t *testing.T) {
	doc := loadCanonicalFrameworkPorts(t)
	if defaultFrameworkFallbackPort != doc.FallbackPort {
		t.Errorf("defaultFrameworkFallbackPort = %d, canonical file has _fallback_port = %d", defaultFrameworkFallbackPort, doc.FallbackPort)
	}
}
