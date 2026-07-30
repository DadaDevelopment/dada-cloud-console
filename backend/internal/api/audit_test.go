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
