package portainer

import (
	"encoding/binary"
	"testing"
)

func frame(stream byte, payload string) []byte {
	b := make([]byte, 8+len(payload))
	b[0] = stream
	binary.BigEndian.PutUint32(b[4:8], uint32(len(payload)))
	copy(b[8:], payload)
	return b
}

func TestDemuxDockerStream_Multiplexed(t *testing.T) {
	in := append(frame(1, "hello\n"), frame(2, "err\n")...)
	got := string(demuxDockerStream(in))
	if got != "hello\nerr\n" {
		t.Fatalf("got %q", got)
	}
}

func TestDemuxDockerStream_RawTTY(t *testing.T) {
	// Plain text (no valid 8-byte frame header) is returned unchanged.
	in := []byte("just plain log text without headers")
	got := string(demuxDockerStream(in))
	if got != string(in) {
		t.Fatalf("expected passthrough, got %q", got)
	}
}

func TestNew_DisabledWhenUnconfigured(t *testing.T) {
	if New("", "") != nil {
		t.Fatal("expected nil client when unconfigured")
	}
	if New("https://portainer", "") != nil {
		t.Fatal("expected nil client when token missing")
	}
	if New("https://portainer", "tok") == nil {
		t.Fatal("expected client when configured")
	}
}
