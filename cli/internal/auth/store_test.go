package auth

import (
	"os"
	"runtime"
	"testing"
	"time"
)

func TestSaveLoadTokenRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tok := StoredToken{
		AccessToken:  "acc",
		RefreshToken: "ref",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(time.Hour),
		ClientID:     "ddc-cli",
		Issuer:       "https://id.dada-tuda.ru/realms/master",
	}
	if err := SaveToken(tok); err != nil {
		t.Fatal(err)
	}

	loaded, ok, err := LoadToken()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected token to be found")
	}
	if loaded.AccessToken != tok.AccessToken || loaded.RefreshToken != tok.RefreshToken {
		t.Fatalf("round trip mismatch: %+v", loaded)
	}

	if runtime.GOOS != "windows" {
		path, err := tokenPath()
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("expected 0600 permissions, got %o", perm)
		}
	}
}

func TestLoadTokenMissingIsNotError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, ok, err := LoadToken()
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no token to be found")
	}
}

func TestStoredTokenExpired(t *testing.T) {
	future := StoredToken{ExpiresAt: time.Now().Add(time.Hour)}
	if future.Expired() {
		t.Error("token expiring in an hour should not be expired")
	}
	past := StoredToken{ExpiresAt: time.Now().Add(-time.Hour)}
	if !past.Expired() {
		t.Error("token expired an hour ago should be expired")
	}
	soon := StoredToken{ExpiresAt: time.Now().Add(5 * time.Second)}
	if !soon.Expired() {
		t.Error("token expiring in 5 seconds should count as expired (30s margin)")
	}
}
