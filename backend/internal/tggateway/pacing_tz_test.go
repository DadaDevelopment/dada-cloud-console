package tggateway

import (
	"testing"
	"time"
)

func TestGatewayLocation_ResolvesIANAWithoutSystemZoneinfo(t *testing.T) {
	t.Setenv("ZONEINFO", t.TempDir())
	t.Setenv("TG_GATEWAY_TZ", "Europe/Moscow")
	loc := GatewayLocation()
	if loc.String() != "Europe/Moscow" {
		t.Fatalf("got %s, want Europe/Moscow", loc)
	}
	if _, off := time.Date(2026, 6, 1, 12, 0, 0, 0, loc).Zone(); off != 3*3600 {
		t.Fatalf("offset %d, want 10800", off)
	}
}
