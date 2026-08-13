package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassifyPollResponse(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		errCode      string
		interval     int
		wantRetry    bool
		wantInterval int
		wantTerminal bool
	}{
		{"success", http.StatusOK, "", 5, false, 0, false},
		{"pending keeps interval", http.StatusBadRequest, "authorization_pending", 5, true, 5, false},
		{"slow_down adds five seconds", http.StatusBadRequest, "slow_down", 5, true, 10, false},
		{"slow_down compounds", http.StatusBadRequest, "slow_down", 10, true, 15, false},
		{"expired_token is terminal", http.StatusBadRequest, "expired_token", 5, false, 0, true},
		{"access_denied is terminal", http.StatusBadRequest, "access_denied", 5, false, 0, true},
		{"unknown error is terminal", http.StatusBadRequest, "invalid_grant", 5, false, 0, true},
		{"empty error body is terminal", http.StatusInternalServerError, "", 5, false, 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := classifyPollResponse(tc.status, tc.errCode, tc.interval)
			if out.retry != tc.wantRetry {
				t.Errorf("retry = %v, want %v", out.retry, tc.wantRetry)
			}
			if tc.wantRetry && out.nextInterval != tc.wantInterval {
				t.Errorf("nextInterval = %d, want %d", out.nextInterval, tc.wantInterval)
			}
			if (out.terminal != nil) != tc.wantTerminal {
				t.Errorf("terminal = %v, want present=%v", out.terminal, tc.wantTerminal)
			}
		})
	}
}

func TestJoinAndParseScopesRoundTrip(t *testing.T) {
	scopes := []string{"read", "builds:read", "deploy:write"}
	joined := JoinScopes(scopes)
	if joined != "read builds:read deploy:write" {
		t.Fatalf("JoinScopes = %q", joined)
	}
	parsed := ParseScopes(joined)
	if len(parsed) != len(scopes) {
		t.Fatalf("ParseScopes = %v, want %v", parsed, scopes)
	}
	for i, s := range scopes {
		if parsed[i] != s {
			t.Fatalf("ParseScopes[%d] = %q, want %q", i, parsed[i], s)
		}
	}
}

func TestParseScopesHandlesMessyWhitespace(t *testing.T) {
	got := ParseScopes("  read   builds:read\tdeploy:write  ")
	want := []string{"read", "builds:read", "deploy:write"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestRequiredScopesIncludesDeployWrite(t *testing.T) {
	found := false
	for _, s := range RequiredScopes {
		if s == "deploy:write" {
			found = true
		}
	}
	if !found {
		t.Fatal("RequiredScopes must include deploy:write, or archive upload will 403")
	}
}

func TestEndpointsFromIssuer(t *testing.T) {
	ep := EndpointsFromIssuer("https://id.dada-tuda.ru/realms/master/")
	if ep.DeviceAuthURL != "https://id.dada-tuda.ru/realms/master/protocol/openid-connect/auth/device" {
		t.Errorf("unexpected DeviceAuthURL: %s", ep.DeviceAuthURL)
	}
	if ep.TokenURL != "https://id.dada-tuda.ru/realms/master/protocol/openid-connect/token" {
		t.Errorf("unexpected TokenURL: %s", ep.TokenURL)
	}
}

func TestStartDeviceAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("client_id") != "ddc-cli" {
			t.Errorf("unexpected client_id: %s", r.Form.Get("client_id"))
		}
		if r.Form.Get("scope") != "read builds:read deploy:write" {
			t.Errorf("unexpected scope: %s", r.Form.Get("scope"))
		}
		json.NewEncoder(w).Encode(DeviceCodeResponse{
			DeviceCode:              "dc-1",
			UserCode:                "ABCD-EFGH",
			VerificationURI:         "https://id.dada-tuda.ru/device",
			VerificationURIComplete: "https://id.dada-tuda.ru/device?user_code=ABCD-EFGH",
			ExpiresIn:               600,
			Interval:                5,
		})
	}))
	defer srv.Close()

	dc, err := StartDeviceAuth(context.Background(), srv.Client(), Endpoints{DeviceAuthURL: srv.URL}, "ddc-cli", JoinScopes(RequiredScopes))
	if err != nil {
		t.Fatal(err)
	}
	if dc.UserCode != "ABCD-EFGH" || dc.DeviceCode != "dc-1" {
		t.Fatalf("unexpected device code response: %+v", dc)
	}
}

func TestPollTokenSucceedsAfterPending(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(tokenErrorResponse{Error: "authorization_pending"})
			return
		}
		json.NewEncoder(w).Encode(TokenResponse{AccessToken: "tok", RefreshToken: "ref", ExpiresIn: 300})
	}))
	defer srv.Close()

	dc := &DeviceCodeResponse{DeviceCode: "dc-1", ExpiresIn: 60, Interval: 0}
	tok, err := PollToken(context.Background(), srv.Client(), Endpoints{TokenURL: srv.URL}, "ddc-cli", dc)
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "tok" {
		t.Fatalf("unexpected token: %+v", tok)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestPollTokenExpires(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(tokenErrorResponse{Error: "authorization_pending"})
	}))
	defer srv.Close()

	dc := &DeviceCodeResponse{DeviceCode: "dc-1", ExpiresIn: 0, Interval: 0}
	_, err := PollToken(context.Background(), srv.Client(), Endpoints{TokenURL: srv.URL}, "ddc-cli", dc)
	if err == nil {
		t.Fatal("expected expiry error")
	}
}

func TestPollTokenAccessDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(tokenErrorResponse{Error: "access_denied"})
	}))
	defer srv.Close()

	dc := &DeviceCodeResponse{DeviceCode: "dc-1", ExpiresIn: 60, Interval: 0}
	_, err := PollToken(context.Background(), srv.Client(), Endpoints{TokenURL: srv.URL}, "ddc-cli", dc)
	if err == nil {
		t.Fatal("expected access_denied error")
	}
}
