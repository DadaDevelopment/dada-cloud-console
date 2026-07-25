package yookassa

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func newTestOAuthClient(t *testing.T, oauthHandler, apiHandler http.HandlerFunc) *OAuthClient {
	t.Helper()
	c := &OAuthClient{HTTPClient: http.DefaultClient}
	if oauthHandler != nil {
		oauthSrv := httptest.NewServer(oauthHandler)
		t.Cleanup(oauthSrv.Close)
		c.OAuthBaseURL = oauthSrv.URL
		c.HTTPClient = oauthSrv.Client()
	}
	if apiHandler != nil {
		apiSrv := httptest.NewServer(apiHandler)
		t.Cleanup(apiSrv.Close)
		c.APIBaseURL = apiSrv.URL
		c.HTTPClient = apiSrv.Client()
	}
	return c
}

func TestOAuthClient_AuthorizeURL(t *testing.T) {
	c := &OAuthClient{OAuthBaseURL: "https://example.test/oauth/v2"}
	got := c.AuthorizeURL("client-1", "state with spaces")
	want := "https://example.test/oauth/v2/authorize?response_type=code&client_id=client-1&state=state+with+spaces"
	if got != want {
		t.Fatalf("AuthorizeURL=%q want %q", got, want)
	}
}

func TestOAuthClient_ExchangeCode_BasicAuthAndForm(t *testing.T) {
	var gotAuth, gotContentType, gotMethod, gotPath string
	var gotForm url.Values
	c := newTestOAuthClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = r.ParseForm()
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "tok_abc", ExpiresIn: 3600})
	}, nil)

	token, expiresIn, err := c.ExchangeCode(context.Background(), "client_1", "secret_1", "code_xyz")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if token != "tok_abc" || expiresIn != 3600 {
		t.Fatalf("token=%q expiresIn=%d want tok_abc/3600", token, expiresIn)
	}
	if gotMethod != http.MethodPost || gotPath != "/token" {
		t.Fatalf("method=%s path=%s want POST /token", gotMethod, gotPath)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("client_1:secret_1"))
	if gotAuth != wantAuth {
		t.Fatalf("Authorization=%q want %q", gotAuth, wantAuth)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("Content-Type=%q want form-urlencoded", gotContentType)
	}
	if gotForm.Get("grant_type") != "authorization_code" || gotForm.Get("code") != "code_xyz" {
		t.Fatalf("form=%v want grant_type=authorization_code code=code_xyz", gotForm)
	}
}

func TestOAuthClient_ExchangeCode_NonSuccessStatus(t *testing.T) {
	c := newTestOAuthClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}, nil)

	_, _, err := c.ExchangeCode(context.Background(), "client_1", "secret_1", "bad_code")
	if err == nil {
		t.Fatal("expected error for 400 response, got nil")
	}
}

func TestOAuthClient_Me_AccountIDField(t *testing.T) {
	c := newTestOAuthClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok_1" {
			t.Errorf("Authorization=%q want Bearer tok_1", got)
		}
		if r.Method != http.MethodGet || r.URL.Path != "/me" {
			t.Errorf("method=%s path=%s want GET /me", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"account_id":"acc_1","extra":"field"}`))
	})

	accountID, raw, err := c.Me(context.Background(), "tok_1")
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if accountID != "acc_1" {
		t.Fatalf("accountID=%q want acc_1", accountID)
	}
	if len(raw) == 0 {
		t.Fatal("raw body should not be empty")
	}
}

func TestOAuthClient_Me_IDField(t *testing.T) {
	c := newTestOAuthClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"id_2"}`))
	})

	accountID, _, err := c.Me(context.Background(), "tok_2")
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if accountID != "id_2" {
		t.Fatalf("accountID=%q want id_2", accountID)
	}
}

func TestOAuthClient_Me_ShopIDField(t *testing.T) {
	c := newTestOAuthClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"shop_id":"shop_3"}`))
	})

	accountID, _, err := c.Me(context.Background(), "tok_3")
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if accountID != "shop_3" {
		t.Fatalf("accountID=%q want shop_3", accountID)
	}
}

func TestOAuthClient_RegisterWebhook_HeadersAndBody(t *testing.T) {
	var gotAuth, gotIdempotence, gotMethod, gotPath, gotContentType string
	var gotBody webhookRequest
	c := newTestOAuthClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotIdempotence = r.Header.Get("Idempotence-Key")
		gotContentType = r.Header.Get("Content-Type")
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(webhookResponse{ID: "wh_1"})
	})

	id, err := c.RegisterWebhook(context.Background(), "tok_1", "payment.succeeded", "https://app.example/yookassa/webhook")
	if err != nil {
		t.Fatalf("RegisterWebhook: %v", err)
	}
	if id != "wh_1" {
		t.Fatalf("id=%q want wh_1", id)
	}
	if gotMethod != http.MethodPost || gotPath != "/webhooks" {
		t.Fatalf("method=%s path=%s want POST /webhooks", gotMethod, gotPath)
	}
	if gotAuth != "Bearer tok_1" {
		t.Fatalf("Authorization=%q want Bearer tok_1", gotAuth)
	}
	if gotIdempotence == "" {
		t.Fatal("Idempotence-Key header must be set")
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type=%q want application/json", gotContentType)
	}
	if gotBody.Event != "payment.succeeded" || gotBody.URL != "https://app.example/yookassa/webhook" {
		t.Fatalf("body=%+v want event=payment.succeeded url set", gotBody)
	}
}

func TestOAuthClient_DeleteWebhook_HeadersAndPath(t *testing.T) {
	var gotAuth, gotIdempotence, gotMethod, gotPath string
	c := newTestOAuthClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotIdempotence = r.Header.Get("Idempotence-Key")
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})

	if err := c.DeleteWebhook(context.Background(), "tok_1", "wh_1"); err != nil {
		t.Fatalf("DeleteWebhook: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/webhooks/wh_1" {
		t.Fatalf("method=%s path=%s want DELETE /webhooks/wh_1", gotMethod, gotPath)
	}
	if gotAuth != "Bearer tok_1" {
		t.Fatalf("Authorization=%q want Bearer tok_1", gotAuth)
	}
	if gotIdempotence == "" {
		t.Fatal("Idempotence-Key header must be set")
	}
}

func TestOAuthClient_DeleteWebhook_NonSuccessStatus(t *testing.T) {
	c := newTestOAuthClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not_found"}`))
	})

	err := c.DeleteWebhook(context.Background(), "tok_1", "missing")
	if err == nil {
		t.Fatal("expected error for 404 response, got nil")
	}
}

func TestEncodeDecodeAccessToken_RoundTrip(t *testing.T) {
	orig := []byte{0x01, 0x02, 0xff, 0x00, 0x7f}
	encoded := EncodeAccessToken(orig)
	decoded, err := DecodeAccessToken(encoded)
	if err != nil {
		t.Fatalf("DecodeAccessToken: %v", err)
	}
	if string(decoded) != string(orig) {
		t.Fatalf("decoded=%v want %v", decoded, orig)
	}
}
