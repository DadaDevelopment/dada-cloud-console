package api

import "testing"

// TestServesHTTP locks the surrogate-domain gate: web ports keep the auto
// default hostname, datastore TCP ports skip it (they would 502). Regression
// guard for the top-decker redis:latest dead-URL case.
func TestServesHTTP(t *testing.T) {
	httpPorts := []int{80, 8080, 3000, 5000, 5173, 8000, 4200, 443, 9200}
	for _, p := range httpPorts {
		if !servesHTTP(p) {
			t.Errorf("servesHTTP(%d) = false, want true (web port must keep default domain)", p)
		}
	}
	dbPorts := []int{6379, 5432, 5433, 3306, 1433, 27017, 5672, 9092, 11211, 2181, 26257}
	for _, p := range dbPorts {
		if servesHTTP(p) {
			t.Errorf("servesHTTP(%d) = true, want false (datastore port must skip 502 default domain)", p)
		}
	}
}
