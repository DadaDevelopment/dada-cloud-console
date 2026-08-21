package frameworkports

import (
	"encoding/json"
	"os"
	"testing"
)

// canonicalFile is the single source of truth every module's in-code table
// must mirror. Backend, build-agent, and gitops-agent each keep an
// independent copy plus this same style of test, so a drift in any one
// module fails only that module's CI.
const canonicalFile = "../../../config/platform/framework-ports.json"

type canonicalDoc struct {
	FallbackPort int            `json:"_fallback_port"`
	Ports        map[string]int `json:"ports"`
}

func loadCanonical(t *testing.T) canonicalDoc {
	t.Helper()
	raw, err := os.ReadFile(canonicalFile)
	if err != nil {
		t.Fatalf("read canonical framework-ports.json: %v", err)
	}
	var doc canonicalDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse canonical framework-ports.json: %v", err)
	}
	return doc
}

func TestPortsMatchCanonicalFile(t *testing.T) {
	doc := loadCanonical(t)

	for framework, wantPort := range doc.Ports {
		gotPort, ok := Ports[framework]
		if !ok {
			t.Errorf("frameworkports.Ports is missing %q (canonical file has port %d)", framework, wantPort)
			continue
		}
		if gotPort != wantPort {
			t.Errorf("frameworkports.Ports[%q] = %d, canonical file has %d", framework, gotPort, wantPort)
		}
	}

	for framework := range Ports {
		if _, ok := doc.Ports[framework]; !ok {
			t.Errorf("frameworkports.Ports has extra entry %q not present in canonical file", framework)
		}
	}
}

func TestFallbackPortMatchesCanonicalFile(t *testing.T) {
	doc := loadCanonical(t)
	if FallbackPort != doc.FallbackPort {
		t.Errorf("FallbackPort = %d, canonical file has _fallback_port = %d", FallbackPort, doc.FallbackPort)
	}
}
