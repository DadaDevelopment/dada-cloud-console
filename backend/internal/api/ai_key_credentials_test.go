package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/dada-tuda/console/backend/internal/auth"
)

func newPublicModelServer(t *testing.T, handler http.Handler) (string, *http.Client) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	transport := srv.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test server certificate is issued for 127.0.0.1
	serverAddr := srv.Listener.Addr().String()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, serverAddr)
	}
	client := &http.Client{Transport: transport, Timeout: srv.Client().Timeout}
	oldLookup := aiModelLookupIP
	aiModelLookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	t.Cleanup(func() { aiModelLookupIP = oldLookup })
	return "https://models.public.test", client
}

func TestNormalizeAIKeyCredentialInput(t *testing.T) {
	negative := -1
	tests := []struct {
		name    string
		input   aiKeyCredentialInput
		wantErr string
	}{
		{name: "valid", input: aiKeyCredentialInput{Provider: " OpenAI ", APIKey: " secret ", Label: " primary ", APIBase: " https://api.example/v1 "}},
		{name: "missing provider", input: aiKeyCredentialInput{APIKey: "secret"}, wantErr: "provider is required"},
		{name: "missing key", input: aiKeyCredentialInput{Provider: "openai"}, wantErr: "api_key is required"},
		{name: "negative priority", input: aiKeyCredentialInput{Provider: "openai", APIKey: "secret", Priority: &negative}, wantErr: "priority must be non-negative"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeAIKeyCredentialInput(tc.input)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err=%v want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			if got.Provider != "openai" || got.APIKey != "secret" || got.Label != "primary" || got.APIBase != "https://api.example/v1" {
				t.Fatalf("normalization failed: %#v", got)
			}
		})
	}
}

func TestAIKeyCredentialPublicShapeNeverSerializesSecret(t *testing.T) {
	b, err := json.Marshal(aiKeyCredentialListItem{
		Provider: "openai",
		Label:    "primary",
		KeyHint:  "sk-p...mnop",
		Enabled:  true,
		Priority: 10,
		Source:   "pool",
		Scope:    "platform",
		Editable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "api_key") || strings.Contains(string(b), "secret") {
		t.Fatalf("public response leaked secret-bearing field: %s", b)
	}
	if !strings.Contains(string(b), `"key_hint":"sk-p...mnop"`) || !strings.Contains(string(b), `"source":"pool"`) {
		t.Fatalf("public response lost safe inventory metadata: %s", b)
	}
}

func TestAIKeyCredentialUpdateRequiresAtLeastOneField(t *testing.T) {
	if err := validateAIKeyCredentialUpdate(aiKeyCredentialUpdateRequest{}); err == nil {
		t.Fatal("empty update must be rejected")
	}
	enabled := false
	if err := validateAIKeyCredentialUpdate(aiKeyCredentialUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enabled-only update rejected: %v", err)
	}
}

func TestDiscoverUpstreamModelsOpenAICompatible(t *testing.T) {
	var gotAuth string
	baseURL, client := newPublicModelServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path=%q want /v1/models", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-5.6-sol"},{"id":"gpt-5.6-sol"},{"id":"invalid display name"},{"id":"embed-1"}]}`))
	}))

	got, err := discoverUpstreamModels(context.Background(), client, "sotamodel", baseURL, "top-secret")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if strings.Join(got, ",") != "gpt-5.6-sol,embed-1" {
		t.Fatalf("models=%v", got)
	}
	if gotAuth != "Bearer top-secret" {
		t.Fatalf("Authorization=%q", gotAuth)
	}
}

func TestDiscoverUpstreamModelsAnthropicPaginationAndHeaders(t *testing.T) {
	requests := 0
	baseURL, client := newPublicModelServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("x-api-key") != "anthropic-secret" || r.Header.Get("anthropic-version") == "" {
			t.Fatalf("missing Anthropic auth/version headers")
		}
		switch r.URL.Query().Get("after_id") {
		case "":
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-a"}],"has_more":true,"last_id":"claude-a"}`))
		case "claude-a":
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-b"}],"has_more":false}`))
		default:
			t.Fatalf("unexpected after_id=%q", r.URL.Query().Get("after_id"))
		}
	}))

	got, err := discoverUpstreamModels(context.Background(), client, "anthropic", baseURL, "anthropic-secret")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if strings.Join(got, ",") != "claude-a,claude-b" || requests != 2 {
		t.Fatalf("models=%v requests=%d", got, requests)
	}
}

func TestDiscoverUpstreamModelsRejectsNonSuccessWithoutLeakingKey(t *testing.T) {
	baseURL, client := newPublicModelServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream rejected top-secret", http.StatusUnauthorized)
	}))

	_, err := discoverUpstreamModels(context.Background(), client, "openai", baseURL, "top-secret")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "top-secret") {
		t.Fatalf("error leaked key: %v", err)
	}
}

func TestValidateAIAPIBaseRejectsUnsafeTargets(t *testing.T) {
	oldLookup := aiModelLookupIP
	aiModelLookupIP = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host == "localhost" {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	t.Cleanup(func() { aiModelLookupIP = oldLookup })
	for _, raw := range []string{
		"http://models.example/v1",
		"https://localhost/v1",
		"https://127.0.0.1/v1",
		"https://10.0.0.1/v1",
		"https://192.168.1.1/v1",
		"https://169.254.169.254/latest/meta-data",
		"https://user:pass@models.example/v1",
		"https://models.example/v1#fragment",
	} {
		if err := validateAIAPIBase(context.Background(), raw); err == nil {
			t.Errorf("unsafe api_base accepted: %s", raw)
		}
	}
	if err := validateAIAPIBase(context.Background(), "https://models.example/v1"); err != nil {
		t.Fatalf("public custom base rejected: %v", err)
	}
}

func TestDiscoverUpstreamModelsDoesNotFollowRedirect(t *testing.T) {
	requests := 0
	baseURL, client := newPublicModelServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Redirect(w, &http.Request{}, "https://127.0.0.1/private", http.StatusFound)
	}))
	_, err := discoverUpstreamModels(context.Background(), client, "openai", baseURL, "secret")
	if err == nil || requests != 1 {
		t.Fatalf("err=%v requests=%d want one rejected redirect", err, requests)
	}
}

func TestDiscoveredModelSnapshotBounds(t *testing.T) {
	tooLong := strings.Repeat("a", aiModelDiscoveryMaxModelIDLen+1)
	for _, invalid := range []string{"model with spaces", "model\nheader", tooLong} {
		if _, err := normalizeDiscoveredModels([]string{invalid}); err == nil {
			t.Errorf("invalid model id accepted: %q", invalid)
		}
	}
	overflow := make([]string, aiModelDiscoveryMaxModels+1)
	for i := range overflow {
		overflow[i] = fmt.Sprintf("model-%d", i)
	}
	if _, err := normalizeDiscoveredModels(overflow); err == nil {
		t.Fatal("oversized model snapshot accepted")
	}
	if aiModelDiscoveryMaxPages > 20 {
		t.Fatalf("page bound=%d is too high", aiModelDiscoveryMaxPages)
	}
}

func TestRequireAIPlatformAdminRejectsCustomerAndAllowsPlatformAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		claims *auth.Claims
		wantOK bool
		status int
	}{
		{name: "unauthenticated", status: http.StatusUnauthorized},
		{name: "customer", claims: &auth.Claims{UserID: uuid.New(), Groups: []string{"/orgs/acme/Owner"}}, status: http.StatusForbidden},
		{name: "platform admin", claims: &auth.Claims{UserID: uuid.New(), Groups: []string{"/platform-admins"}}, wantOK: true, status: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			if tc.claims != nil {
				auth.SetClaims(c, tc.claims)
			}
			_, ok := requireAIPlatformAdmin(c)
			if ok != tc.wantOK || w.Code != tc.status {
				t.Fatalf("ok=%v status=%d want ok=%v status=%d", ok, w.Code, tc.wantOK, tc.status)
			}
		})
	}
}

func TestPublicModelShapeHidesPlatformCredentialIDs(t *testing.T) {
	b, err := json.Marshal(aiKeyPublicModelListItem{ID: "gpt-5.6-sol", Provider: "sotamodel"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "credential") {
		t.Fatalf("public model leaked credential identity: %s", b)
	}
}

func TestSotaModelIsAvailableToPlatformPoolAdmin(t *testing.T) {
	if !isKnownAIProvider("sotamodel") {
		t.Fatal("sotamodel must be selectable for the global platform pool")
	}
}

func TestAdminCredentialRejectsUnknownProvider(t *testing.T) {
	if err := validateAdminAIProvider("made-up-provider"); err == nil || err.Error() != "unknown provider" {
		t.Fatalf("err=%v want unknown provider", err)
	}
	if err := validateAdminAIProvider(" SotaModel "); err != nil {
		t.Fatalf("known provider rejected: %v", err)
	}
}

func TestReplaceCredentialModelsAcceptsGlobalPlatformCredential(t *testing.T) {
	pool := testAICredPool(t)
	ctx := context.Background()
	var credentialID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO ai_gateway_key_credentials
		(gateway_key_id,provider,label,api_key_encrypted) VALUES (NULL,$1,'test','\x74657374'::bytea) RETURNING id`,
		"test-global-"+uuid.NewString()[:8]).Scan(&credentialID); err != nil {
		t.Fatalf("insert global credential: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM ai_gateway_key_credentials WHERE id=$1`, credentialID)
	})

	if err := replaceCredentialModels(ctx, pool, credentialID, []string{"gpt-5.6-sol", "gpt-5.6-sol"}); err != nil {
		t.Fatalf("replace global credential models: %v", err)
	}
	var count int
	var status string
	if err := pool.QueryRow(ctx, `SELECT count(*),max(c.status) FROM ai_gateway_key_credential_models m JOIN ai_gateway_key_credentials c ON c.id=m.credential_id WHERE m.credential_id=$1`, credentialID).Scan(&count, &status); err != nil {
		t.Fatalf("count models: %v", err)
	}
	if count != 1 || status != "healthy" {
		t.Fatalf("model count=%d status=%q want deduplicated 1 and healthy", count, status)
	}
}

func TestModelDiscoveryDoesNotHealInferenceCooldown(t *testing.T) {
	pool := testAICredPool(t)
	ctx := context.Background()
	var credentialID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO ai_gateway_key_credentials
		(gateway_key_id,provider,label,api_key_encrypted,status,unavailable_until)
		VALUES (NULL,'openai','catalog-only','\x74657374'::bytea,'cooldown',now()+interval '1 hour')
		RETURNING id`).Scan(&credentialID); err != nil {
		t.Fatalf("insert credential: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM ai_gateway_key_credentials WHERE id=$1`, credentialID)
	})

	if err := replaceCredentialModels(ctx, pool, credentialID, []string{"gpt-4o"}); err != nil {
		t.Fatalf("store discovery: %v", err)
	}
	var status string
	var unavailable bool
	if err := pool.QueryRow(ctx, `SELECT status,unavailable_until > now()
		FROM ai_gateway_key_credentials WHERE id=$1`, credentialID).Scan(&status, &unavailable); err != nil {
		t.Fatal(err)
	}
	if status != "cooldown" || !unavailable {
		t.Fatalf("catalog discovery healed inference state: status=%q unavailable=%v", status, unavailable)
	}
}

func TestUpdateRediscoveryReplacesStaleGlobalModels(t *testing.T) {
	pool := testAICredPool(t)
	ctx := context.Background()
	var credentialID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO ai_gateway_key_credentials
		(gateway_key_id,provider,label,api_key_encrypted) VALUES (NULL,'sotamodel','update-test','\x74657374'::bytea) RETURNING id`).Scan(&credentialID); err != nil {
		t.Fatalf("insert global credential: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM ai_gateway_key_credentials WHERE id=$1`, credentialID)
	})
	if err := replaceCredentialModels(ctx, pool, credentialID, []string{"stale-model"}); err != nil {
		t.Fatalf("seed stale models: %v", err)
	}

	baseURL, client := newPublicModelServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer replacement-key" {
			t.Fatalf("rediscovery used wrong credential")
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.6-sol"}]}`))
	}))
	oldClient := aiModelDiscoveryClient
	aiModelDiscoveryClient = client
	t.Cleanup(func() { aiModelDiscoveryClient = oldClient })

	refreshCredentialDiscovery(ctx, pool, createdCredentialDiscovery{ID: credentialID, Provider: "sotamodel", APIBase: baseURL, APIKey: "replacement-key"})
	var models []string
	rows, err := pool.Query(ctx, `SELECT model_id FROM ai_gateway_key_credential_models WHERE credential_id=$1 ORDER BY model_id`, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			t.Fatal(err)
		}
		models = append(models, model)
	}
	if strings.Join(models, ",") != "gpt-5.6-sol,sota-opus,sota-opus-max,sota-opus-xhigh" {
		t.Fatalf("models=%v want refreshed wire model plus stable Sota aliases", models)
	}
}

func TestSotaAliasesUseEffectiveProviderCatalog(t *testing.T) {
	got := strings.Join(aiAliasesForProvider("sotamodel"), ",")
	if got != "sota-opus,sota-opus-xhigh,sota-opus-max" {
		t.Fatalf("sotamodel aliases=%q", got)
	}
	if strings.Contains(strings.Join(aiAliasesForProvider("openai"), ","), "sota-opus") {
		t.Fatal("OpenAI-compatible wire transport must not own Sota credentials")
	}
}

func TestPublicModelAllowlistContainsOnlyCuratedAliases(t *testing.T) {
	aliases := knownAIAliases()
	if len(aliases) != len(aiCatalogModels) {
		t.Fatalf("aliases=%d catalog=%d", len(aliases), len(aiCatalogModels))
	}
	for _, raw := range []string{"gpt-5.6-sol", "claude-sonnet-5", "meta/llama-3.1-405b"} {
		if slices.Contains(aliases, raw) {
			t.Fatalf("raw upstream model %q leaked into public allowlist", raw)
		}
	}
}
