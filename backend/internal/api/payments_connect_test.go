package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/billing/yookassa"
	"github.com/dada-tuda/console/backend/internal/config"
)

func testPaymentsConnectConfig() *config.Config {
	return &config.Config{
		GitopsEncryptionKey:         "ab000000000000000000000000000000000000000000000000000000000000cd",
		YooKassaPartnerClientID:     "partner_client_1",
		YooKassaPartnerClientSecret: "partner_secret_1",
	}
}

func seedPaymentsEnv(t *testing.T, pool *pgxpool.Pool) (projectID, envID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"payments-connect-test-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, projectID) })

	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "ns-pc-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	return projectID, envID
}

func newPaymentsCtx(method, path string, claims *auth.Claims, p gin.Params) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, path, nil)
	c.Params = p
	if claims != nil {
		auth.SetClaims(c, claims)
	}
	return c, rec
}

func appParams(projectID, envID uuid.UUID, appName string) gin.Params {
	return gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: appName},
	}
}

func newTestOAuthHandlerClient(t *testing.T) *yookassa.OAuthClient {
	t.Helper()
	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "tok_" + uuid.NewString()[:8],
			"expires_in":   3600,
		})
	}))
	t.Cleanup(oauthSrv.Close)

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/me":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"account_id":"acc_test_1"}`))
		case r.URL.Path == "/webhooks" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "wh_" + uuid.NewString()[:8]})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(apiSrv.Close)

	return &yookassa.OAuthClient{
		OAuthBaseURL: oauthSrv.URL,
		APIBaseURL:   apiSrv.URL,
		HTTPClient:   http.DefaultClient,
	}
}

func TestPaymentsConnect_Unconfigured_409(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, envID := seedPaymentsEnv(t, pool)
	userID := seedUser(t, pool)
	seedApp(t, pool, projectID, envID, "app1")

	h := &Handler{pool: pool, cfg: &config.Config{}, yookassaOAuth: yookassa.NewOAuthClient()}
	c, rec := newPaymentsCtx(http.MethodPost, "/", godClaims(userID), appParams(projectID, envID, "app1"))
	h.PaymentsConnect(c)

	if rec.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s want 409 payments_not_configured", rec.Code, rec.Body.String())
	}
}

func TestPaymentsConnect_Success_InsertsStateRow(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, envID := seedPaymentsEnv(t, pool)
	userID := seedUser(t, pool)
	seedApp(t, pool, projectID, envID, "app1")

	h := &Handler{pool: pool, cfg: testPaymentsConnectConfig(), yookassaOAuth: yookassa.NewOAuthClient()}
	c, rec := newPaymentsCtx(http.MethodPost, "/", godClaims(userID), appParams(projectID, envID, "app1"))
	h.PaymentsConnect(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	parsed, err := url.Parse(resp.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize_url: %v", err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatal("authorize_url missing state param")
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM payment_oauth_states WHERE state = $1 AND project_id = $2 AND environment_id = $3 AND app_name = 'app1'`,
		state, projectID, envID,
	).Scan(&count); err != nil {
		t.Fatalf("query state row: %v", err)
	}
	if count != 1 {
		t.Fatalf("payment_oauth_states rows for state=%d want 1", count)
	}
}

func seedPaymentsState(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName, userSub string, createdAt time.Time) string {
	t.Helper()
	state := "test-state-" + uuid.NewString()[:12]
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO payment_oauth_states (state, project_id, environment_id, app_name, user_sub, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		state, projectID, envID, appName, userSub, createdAt,
	); err != nil {
		t.Fatalf("seed oauth state: %v", err)
	}
	return state
}

func TestPaymentsCallback_SuccessPath_WritesConnectionAndEnvVars(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, envID := seedPaymentsEnv(t, pool)
	userID := seedUser(t, pool)
	seedApp(t, pool, projectID, envID, "app1")
	state := seedPaymentsState(t, pool, projectID, envID, "app1", userID.String(), time.Now())

	h := &Handler{pool: pool, cfg: testPaymentsConnectConfig(), yookassaOAuth: newTestOAuthHandlerClient(t)}
	c, rec := newPaymentsCtx(http.MethodGet, "/?code=code123&state="+state, nil, nil)
	h.PaymentsOAuthCallback(c)

	if rec.Code != http.StatusFound {
		t.Fatalf("code=%d body=%s want 302", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Fatal("missing Location header")
	}

	var accountID string
	var encTok string
	var webhookIDsRaw []byte
	err := pool.QueryRow(context.Background(),
		`SELECT account_id, access_token_enc, webhook_ids FROM payment_connections WHERE environment_id = $1 AND app_name = 'app1'`,
		envID,
	).Scan(&accountID, &encTok, &webhookIDsRaw)
	if err != nil {
		t.Fatalf("query connection row: %v", err)
	}
	if accountID != "acc_test_1" {
		t.Fatalf("account_id=%q want acc_test_1", accountID)
	}
	if encTok == "" {
		t.Fatal("access_token_enc empty")
	}

	var envCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM env_vars WHERE environment_id = $1 AND app_name = 'app1' AND key IN ('YOOKASSA_OAUTH_TOKEN','YOOKASSA_ACCOUNT_ID')`,
		envID,
	).Scan(&envCount); err != nil {
		t.Fatalf("query env_vars: %v", err)
	}
	if envCount != 2 {
		t.Fatalf("env_vars rows=%d want 2", envCount)
	}

	var stateCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM payment_oauth_states WHERE state = $1`, state,
	).Scan(&stateCount); err != nil {
		t.Fatalf("query state row: %v", err)
	}
	if stateCount != 0 {
		t.Fatal("state row should be deleted after use (one-time)")
	}
}

func TestPaymentsCallback_StateIsOneTime(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, envID := seedPaymentsEnv(t, pool)
	userID := seedUser(t, pool)
	seedApp(t, pool, projectID, envID, "app1")
	state := seedPaymentsState(t, pool, projectID, envID, "app1", userID.String(), time.Now())

	h := &Handler{pool: pool, cfg: testPaymentsConnectConfig(), yookassaOAuth: newTestOAuthHandlerClient(t)}

	c1, rec1 := newPaymentsCtx(http.MethodGet, "/?code=code123&state="+state, nil, nil)
	h.PaymentsOAuthCallback(c1)
	if rec1.Code != http.StatusFound || rec1.Header().Get("Location") == "" {
		t.Fatalf("first use: code=%d want 302 success", rec1.Code)
	}

	c2, rec2 := newPaymentsCtx(http.MethodGet, "/?code=code123&state="+state, nil, nil)
	h.PaymentsOAuthCallback(c2)
	loc2 := rec2.Header().Get("Location")
	if rec2.Code != http.StatusFound {
		t.Fatalf("second use: code=%d want 302", rec2.Code)
	}
	if loc2 == "" || !strings.Contains(loc2, "payments_error=invalid_or_expired_state") {
		t.Fatalf("second use of state should be rejected, got Location=%q", loc2)
	}
}

func TestPaymentsCallback_ExpiredStateRejected(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, envID := seedPaymentsEnv(t, pool)
	userID := seedUser(t, pool)
	seedApp(t, pool, projectID, envID, "app1")
	state := seedPaymentsState(t, pool, projectID, envID, "app1", userID.String(), time.Now().Add(-20*time.Minute))

	h := &Handler{pool: pool, cfg: testPaymentsConnectConfig(), yookassaOAuth: newTestOAuthHandlerClient(t)}
	c, rec := newPaymentsCtx(http.MethodGet, "/?code=code123&state="+state, nil, nil)
	h.PaymentsOAuthCallback(c)

	if rec.Code != http.StatusFound {
		t.Fatalf("code=%d want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "payments_error=invalid_or_expired_state") {
		t.Fatalf("Location=%q want payments_error=invalid_or_expired_state", loc)
	}

	var stateCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM payment_oauth_states WHERE state = $1`, state,
	).Scan(&stateCount); err != nil {
		t.Fatalf("query state row: %v", err)
	}
	if stateCount != 0 {
		t.Fatal("expired state must still be consumed (deleted), not left around")
	}
}

func TestPaymentsCallback_Reconnect_Upserts(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, envID := seedPaymentsEnv(t, pool)
	userID := seedUser(t, pool)
	seedApp(t, pool, projectID, envID, "app1")

	h := &Handler{pool: pool, cfg: testPaymentsConnectConfig(), yookassaOAuth: newTestOAuthHandlerClient(t)}

	state1 := seedPaymentsState(t, pool, projectID, envID, "app1", userID.String(), time.Now())
	c1, rec1 := newPaymentsCtx(http.MethodGet, "/?code=code1&state="+state1, nil, nil)
	h.PaymentsOAuthCallback(c1)
	if rec1.Code != http.StatusFound {
		t.Fatalf("first connect: code=%d want 302", rec1.Code)
	}

	var firstConnID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM payment_connections WHERE environment_id = $1 AND app_name = 'app1'`, envID,
	).Scan(&firstConnID); err != nil {
		t.Fatalf("query first connection: %v", err)
	}

	state2 := seedPaymentsState(t, pool, projectID, envID, "app1", userID.String(), time.Now())
	c2, rec2 := newPaymentsCtx(http.MethodGet, "/?code=code2&state="+state2, nil, nil)
	h.PaymentsOAuthCallback(c2)
	if rec2.Code != http.StatusFound {
		t.Fatalf("reconnect: code=%d want 302", rec2.Code)
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM payment_connections WHERE environment_id = $1 AND app_name = 'app1'`, envID,
	).Scan(&count); err != nil {
		t.Fatalf("query connections after reconnect: %v", err)
	}
	if count != 1 {
		t.Fatalf("payment_connections rows=%d want 1 (reconnect must upsert, not duplicate)", count)
	}

	var secondConnID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM payment_connections WHERE environment_id = $1 AND app_name = 'app1'`, envID,
	).Scan(&secondConnID); err != nil {
		t.Fatalf("query second connection: %v", err)
	}
	if secondConnID != firstConnID {
		t.Fatalf("connection id changed on reconnect: %s -> %s; ON CONFLICT DO UPDATE should keep the row's original id", firstConnID, secondConnID)
	}
}

func TestPaymentsDisconnect_RemovesEnvVarsAndRow(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, envID := seedPaymentsEnv(t, pool)
	userID := seedUser(t, pool)
	seedApp(t, pool, projectID, envID, "app1")

	h := &Handler{pool: pool, cfg: testPaymentsConnectConfig(), yookassaOAuth: newTestOAuthHandlerClient(t)}

	state := seedPaymentsState(t, pool, projectID, envID, "app1", userID.String(), time.Now())
	cbCtx, cbRec := newPaymentsCtx(http.MethodGet, "/?code=code1&state="+state, nil, nil)
	h.PaymentsOAuthCallback(cbCtx)
	if cbRec.Code != http.StatusFound {
		t.Fatalf("setup connect: code=%d want 302", cbRec.Code)
	}

	dc, drec := newPaymentsCtx(http.MethodDelete, "/", godClaims(userID), appParams(projectID, envID, "app1"))
	h.PaymentsDisconnect(dc)
	dc.Writer.WriteHeaderNow()
	if drec.Code != http.StatusNoContent {
		t.Fatalf("disconnect: code=%d body=%s want 204", drec.Code, drec.Body.String())
	}

	var envCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM env_vars WHERE environment_id = $1 AND app_name = 'app1' AND key IN ('YOOKASSA_OAUTH_TOKEN','YOOKASSA_ACCOUNT_ID')`,
		envID,
	).Scan(&envCount); err != nil {
		t.Fatalf("query env_vars: %v", err)
	}
	if envCount != 0 {
		t.Fatalf("env_vars rows after disconnect=%d want 0", envCount)
	}

	var connCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM payment_connections WHERE environment_id = $1 AND app_name = 'app1'`, envID,
	).Scan(&connCount); err != nil {
		t.Fatalf("query payment_connections: %v", err)
	}
	if connCount != 0 {
		t.Fatalf("payment_connections rows after disconnect=%d want 0", connCount)
	}
}
