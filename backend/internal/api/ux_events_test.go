package api

import (
	"testing"
	"time"
)

func TestParseUXTimeRejectsImplausibleClocks(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		raw  string
		want time.Time
	}{
		{"empty falls back to server time", "", now},
		{"garbage falls back to server time", "not-a-time", now},
		{"far future falls back to server time", now.Add(time.Hour).Format(time.RFC3339), now},
		{"ancient falls back to server time", now.Add(-48 * time.Hour).Format(time.RFC3339), now},
		{"plausible is kept", now.Add(-90 * time.Second).Format(time.RFC3339), now.Add(-90 * time.Second)},
		{"small forward skew is kept", now.Add(time.Minute).Format(time.RFC3339), now.Add(time.Minute)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseUXTime(tc.raw, now)
			if !got.Equal(tc.want) {
				t.Fatalf("parseUXTime(%q) = %s, want %s", tc.raw, got, tc.want)
			}
		})
	}
}

func TestOptionalUUIDRejectsNonUUID(t *testing.T) {
	if optionalUUID("") != nil {
		t.Fatal("empty id must be nil, not a zero UUID")
	}
	if optionalUUID("someone@example.com") != nil {
		t.Fatal("a non-UUID must be dropped: this column must never hold personal data")
	}
	id := optionalUUID(" 3f1b0f9e-6c1a-4a4e-9a2b-2f7c1d5a8e33 ")
	if id == nil || id.String() != "3f1b0f9e-6c1a-4a4e-9a2b-2f7c1d5a8e33" {
		t.Fatalf("a padded valid UUID must parse, got %v", id)
	}
}

func TestUXEventTypesAreClosed(t *testing.T) {
	if uxEventTypes["arbitrary_event"] {
		t.Fatal("the event-name set must stay closed")
	}
	for _, want := range []string{"session_start", "pageview", "click", "nav_leave"} {
		if !uxEventTypes[want] {
			t.Fatalf("%s must be accepted: the path cannot be reconstructed without it", want)
		}
	}
}
