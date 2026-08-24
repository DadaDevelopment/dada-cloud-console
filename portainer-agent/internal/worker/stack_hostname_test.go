package worker

import (
	"encoding/json"
	"testing"

	"github.com/dada-tuda/console/portainer-agent/internal/db"
)

// TestHostnamePatchFieldsPublishesTheAddress locks the parity the VM publish
// path exists for: a published VM app must carry the same url/url_status keys a
// cluster app does, or the console shows a live site as an app with no address.
func TestHostnamePatchFieldsPublishesTheAddress(t *testing.T) {
	got := hostnamePatchFields(db.PrimaryHostnameInfo{
		Hostname: "harness.dada-tuda.ru",
		Status:   "active",
	})
	if got["url"] != "https://harness.dada-tuda.ru" {
		t.Fatalf("url = %#v, want the https address", got["url"])
	}
	if got["url_status"] != "active" {
		t.Fatalf("url_status = %#v, want active", got["url_status"])
	}
}

// TestHostnamePatchFieldsClearsADetachedAddress covers the merge semantics: the
// patch is concatenated into summary_json, so omitting the keys when there is no
// hostname would leave the console advertising a domain that was detached.
func TestHostnamePatchFieldsClearsADetachedAddress(t *testing.T) {
	got := hostnamePatchFields(db.PrimaryHostnameInfo{})
	for _, key := range []string{"url", "url_status", "url_reason"} {
		v, ok := got[key]
		if !ok {
			t.Fatalf("%s missing, a stale address would survive the merge", key)
		}
		if v != nil {
			t.Fatalf("%s = %#v, want nil", key, v)
		}
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal patch: %v", err)
	}
	if string(encoded) != `{"url":null,"url_reason":null,"url_status":null}` {
		t.Fatalf("patch JSON = %s, want explicit nulls", encoded)
	}
}

// TestHostnamePatchFieldsCarriesTheReason keeps the failure story attached to the
// address: a pending hostname with no reason reads as "just slow", which is the
// difference between a user waiting and a user filing a ticket.
func TestHostnamePatchFieldsCarriesTheReason(t *testing.T) {
	got := hostnamePatchFields(db.PrimaryHostnameInfo{
		Hostname: "harness.dada-tuda.ru",
		Status:   "failed",
		Reason:   "dns_propagation_timeout",
	})
	if got["url_reason"] != "dns_propagation_timeout" {
		t.Fatalf("url_reason = %#v, want the lookup's reason", got["url_reason"])
	}
}
