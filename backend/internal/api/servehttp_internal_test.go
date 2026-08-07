package api

import "testing"

// TestAppNeedsDefaultDomain locks the only public-route rule. A configured
// positive port gets a public HTTP route; protocol cannot be guessed from a
// framework, worker flag, or a well-known port number.
func TestPublicRouteFollowsConfiguredPort(t *testing.T) {
	for _, port := range []float64{80, 8080, 6379, 5432, 8443} {
		if !appNeedsDefaultDomain(map[string]any{"port": port, "worker": true}) {
			t.Errorf("port %v must keep its configured public route", port)
		}
	}
	for _, port := range []float64{0, -1} {
		if appNeedsDefaultDomain(map[string]any{"port": port}) {
			t.Errorf("port %v must have no public route", port)
		}
	}
}
