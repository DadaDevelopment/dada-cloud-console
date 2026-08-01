package api

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAuditSeenCollapsesWithinWindow(t *testing.T) {
	s := newAuditSeen(30 * time.Minute)
	now := time.Now()

	if !s.allow("user-a", now) {
		t.Fatal("first occurrence must be recorded")
	}
	if s.allow("user-a", now.Add(29*time.Minute)) {
		t.Fatal("second occurrence inside the window must be collapsed")
	}
	if !s.allow("user-a", now.Add(31*time.Minute)) {
		t.Fatal("occurrence past the window must be recorded again")
	}
	if !s.allow("user-b", now) {
		t.Fatal("a different key must not be collapsed by another key's window")
	}
}

func TestAuditSeenResetsOnOverflow(t *testing.T) {
	s := newAuditSeen(time.Hour)
	now := time.Now()
	for i := 0; i < auditSeenLimit; i++ {
		s.allow(uuid.New().String(), now)
	}
	if len(s.seen) != auditSeenLimit {
		t.Fatalf("expected tracker filled to the limit, got %d", len(s.seen))
	}
	s.allow("overflow", now)
	if len(s.seen) != 1 {
		t.Fatalf("overflow must drop the map and keep only the newest key, got %d", len(s.seen))
	}
	if !s.allow("overflow-2", now) {
		t.Fatal("tracker must stay usable after an overflow reset")
	}
}

func TestNullableUUID(t *testing.T) {
	if nullableUUID(uuid.Nil) != nil {
		t.Fatal("uuid.Nil must be written as SQL NULL, not as the zero uuid")
	}
	id := uuid.New()
	if nullableUUID(id) != any(id) {
		t.Fatal("a real uuid must be passed through unchanged")
	}
}

func TestAuditVisitsKeepsOneRowPerVisit(t *testing.T) {
	v := newAuditVisits(30 * time.Minute)
	now := time.Now()

	newVisit, reason := v.observe("user-a", "sid-1", now)
	if !newVisit || reason != "first" {
		t.Fatalf("first request must open a visit, got %v/%q", newVisit, reason)
	}
	if newVisit, _ = v.observe("user-a", "sid-1", now.Add(20*time.Minute)); newVisit {
		t.Fatal("continued activity inside the same visit must not open a new one")
	}
	if newVisit, _ = v.observe("user-a", "sid-1", now.Add(40*time.Minute)); newVisit {
		t.Fatal("gap is measured from the last request, not the last recorded row")
	}
	newVisit, reason = v.observe("user-a", "sid-1", now.Add(1*time.Hour+20*time.Minute))
	if !newVisit || reason != "idle" {
		t.Fatalf("a real idle gap must open a new visit, got %v/%q", newVisit, reason)
	}
}

func TestAuditVisitsSeparatesRelogin(t *testing.T) {
	v := newAuditVisits(30 * time.Minute)
	now := time.Now()

	if newVisit, _ := v.observe("user-a", "sid-1", now); !newVisit {
		t.Fatal("first request must open a visit")
	}
	newVisit, reason := v.observe("user-a", "sid-2", now.Add(6*time.Minute))
	if !newVisit || reason != "relogin" {
		t.Fatalf("a new keycloak session six minutes later is a second visit, got %v/%q", newVisit, reason)
	}
	if newVisit, _ := v.observe("user-a", "", now.Add(7*time.Minute)); newVisit {
		t.Fatal("a missing sid must not be read as a session change")
	}
	if newVisit, _ := v.observe("user-b", "sid-9", now.Add(6*time.Minute)); !newVisit {
		t.Fatal("another user's visit is independent")
	}
}

func TestAuditVisitsResetsOnOverflow(t *testing.T) {
	v := newAuditVisits(time.Hour)
	now := time.Now()

	for i := 0; i < auditSeenLimit+1; i++ {
		v.observe(uuid.NewString(), "sid", now)
	}
	if len(v.users) > auditSeenLimit {
		t.Fatalf("tracker must not grow past the cap, got %d", len(v.users))
	}
}
