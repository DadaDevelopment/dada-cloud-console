package beget

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const sampleVPSList = `{"vps":[
  {"id":"c3403bf7-255a-4ff7-8041-33296a0eca65","slug":"test-vm-01","display_name":"test-vm-01",
   "status":"RUNNING","ip_address":"46.173.27.26","date_create":"2026-05-30T03:34:00+03:00",
   "configuration":{"cpu_count":2,"memory":2048,"disk_size":20480,"region":"ru1"}}
]}`

func TestListVPS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok123" {
			t.Errorf("missing/wrong auth header: %q", got)
		}
		if r.URL.Path != "/v1/vps/server/list" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleVPSList))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok123")
	vms, err := c.ListVPS(context.Background())
	if err != nil {
		t.Fatalf("ListVPS: %v", err)
	}
	if len(vms) != 1 {
		t.Fatalf("want 1 vps, got %d", len(vms))
	}
	v := vms[0]
	if v.ID != "c3403bf7-255a-4ff7-8041-33296a0eca65" {
		t.Errorf("id: %q", v.ID)
	}
	if v.Slug != "test-vm-01" {
		t.Errorf("slug: %q", v.Slug)
	}
	if v.IPAddress != "46.173.27.26" {
		t.Errorf("ip: %q", v.IPAddress)
	}
	if v.Configuration.CPUCount != 2 || v.Configuration.Memory != 2048 || v.Configuration.DiskSize != 20480 {
		t.Errorf("configuration: %+v", v.Configuration)
	}
	if v.Configuration.Region != "ru1" {
		t.Errorf("region: %q", v.Configuration.Region)
	}
	wantTime, _ := time.Parse(time.RFC3339, "2026-05-30T03:34:00+03:00")
	if !v.DateCreate.Equal(wantTime) {
		t.Errorf("date_create: got %v want %v", v.DateCreate, wantTime)
	}
}

func TestListVPSErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "bad")
	if _, err := c.ListVPS(context.Background()); err == nil {
		t.Fatal("expected error on non-200 status")
	}
}
