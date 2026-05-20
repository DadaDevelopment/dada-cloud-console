package portainer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/portainer-agent/internal/portainer"
)

func TestGetEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "ptr_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/endpoints/12" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(portainer.Endpoint{ //nolint:errcheck
			ID: 12, Name: "test", Heartbeat: true, LastCheckInDate: 1716123456,
		})
	}))
	defer srv.Close()

	c := portainer.New(srv.URL, "ptr_test")
	ep, err := c.GetEndpoint(context.Background(), 12)
	if err != nil {
		t.Fatalf("GetEndpoint error: %v", err)
	}
	if ep.ID != 12 {
		t.Errorf("expected ID=12, got %d", ep.ID)
	}
	if !portainer.IsAgentConnected(ep) {
		t.Error("expected IsAgentConnected=true")
	}
}

func TestIsAgentConnected(t *testing.T) {
	tests := []struct {
		name      string
		ep        portainer.Endpoint
		connected bool
	}{
		{"heartbeat+checkin", portainer.Endpoint{Heartbeat: true, LastCheckInDate: 100}, true},
		{"status1 only", portainer.Endpoint{Status: 1, Heartbeat: false, LastCheckInDate: 0}, false},
		{"heartbeat without checkin", portainer.Endpoint{Heartbeat: true, LastCheckInDate: 0}, false},
		{"checkin without heartbeat", portainer.Endpoint{Heartbeat: false, LastCheckInDate: 100}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := portainer.IsAgentConnected(&tt.ep)
			if got != tt.connected {
				t.Errorf("IsAgentConnected=%v, want %v", got, tt.connected)
			}
		})
	}
}
