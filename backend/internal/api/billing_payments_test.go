package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/billing/yookassa"
)

func testPaymentsPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping billing-payments DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedPaymentsProject(t *testing.T, pool *pgxpool.Pool, orgID string) uuid.UUID {
	t.Helper()
	var projectID uuid.UUID
	suffix := uuid.NewString()[:8]
	err := pool.QueryRow(context.Background(),
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"payments-test-"+suffix, orgID,
	).Scan(&projectID)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, projectID)
	})
	return projectID
}

func newBillingCtx(method, path, body string, claims *auth.Claims, projectID uuid.UUID) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var bodyReader *bytes.Buffer
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	} else {
		bodyReader = bytes.NewBufferString("")
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}
	if claims != nil {
		auth.SetClaims(c, claims)
	}
	return c, rec
}

func TestBillingCheckout_UnknownPlan_400(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-checkout-400-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)

	h := &Handler{pool: pool, billingPlans: testPlans(), yookassa: nonNilProvider(pool)}
	c, rec := newBillingCtx(http.MethodPost, "/", `{"plan":"does-not-exist"}`, godClaims(uuid.New()), projectID)
	h.BillingCheckout(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s want 400 for unknown plan", rec.Code, rec.Body.String())
	}
}

func TestBillingCheckout_FreePlan_400(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-checkout-free-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)

	h := &Handler{pool: pool, billingPlans: testPlans(), yookassa: nonNilProvider(pool)}
	c, rec := newBillingCtx(http.MethodPost, "/", `{"plan":"free"}`, godClaims(uuid.New()), projectID)
	h.BillingCheckout(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s want 400 for free plan (not payable)", rec.Code, rec.Body.String())
	}
}

func TestBillingCheckout_EnterprisePlan_400(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-checkout-ent-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)

	h := &Handler{pool: pool, billingPlans: testPlans(), yookassa: nonNilProvider(pool)}
	c, rec := newBillingCtx(http.MethodPost, "/", `{"plan":"enterprise"}`, godClaims(uuid.New()), projectID)
	h.BillingCheckout(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s want 400 for enterprise plan (sales-negotiated, not payable)", rec.Code, rec.Body.String())
	}
}

func TestBillingCheckout_ProviderUnconfigured_409(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-checkout-409-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)

	h := &Handler{pool: pool, billingPlans: testPlans(), yookassa: nil}
	c, rec := newBillingCtx(http.MethodPost, "/", `{"plan":"startup"}`, godClaims(uuid.New()), projectID)
	h.BillingCheckout(c)

	if rec.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s want 409 when payments are not configured", rec.Code, rec.Body.String())
	}
}

func TestBillingCheckout_HappyPath_ReturnsConfirmationURL(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-checkout-ok-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})

	client := newFakeYooKassaClient(t, "pending")
	provider := yookassa.NewProvider(pool, client, "https://console.dada-tuda.ru/billing/return", false)
	h := &Handler{pool: pool, billingPlans: testPlans(), yookassa: provider}

	c, rec := newBillingCtx(http.MethodPost, "/", `{"plan":"startup"}`, godClaims(uuid.New()), projectID)
	h.BillingCheckout(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		PaymentID       string `json:"payment_id"`
		ConfirmationURL string `json:"confirmation_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	if resp.PaymentID == "" || resp.ConfirmationURL == "" {
		t.Fatalf("resp=%+v want both payment_id and confirmation_url set", resp)
	}
}

func TestYooKassaWebhook_Unconfigured_StillReturns200(t *testing.T) {
	h := &Handler{yookassa: nil}
	c, rec := newWebhookCtx(t, "", `{"event":"payment.succeeded","object":{"id":"yk_1"}}`)
	h.YooKassaWebhook(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200 even when payments are unconfigured (must not error to YooKassa)", rec.Code, rec.Body.String())
	}
}

func TestYooKassaWebhook_MissingObjectID_400(t *testing.T) {
	h := &Handler{yookassa: nil}
	c, rec := newWebhookCtx(t, "", `{"event":"payment.succeeded","object":{}}`)
	h.YooKassaWebhook(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s want 400 for missing object.id", rec.Code, rec.Body.String())
	}
}

func newFakeYooKassaClient(t *testing.T, status string) *yookassa.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(yookassa.Payment{
			ID:     "yk_fake",
			Status: status,
			Paid:   status == "succeeded",
			Amount: yookassa.Amount{Value: "990.00", Currency: "RUB"},
			Confirmation: yookassa.Confirmation{
				Type: "redirect",
				URL:  "https://yoomoney.ru/checkout/payments/v2/contract?orderId=yk_fake",
			},
		})
	}))
	t.Cleanup(srv.Close)
	c := &yookassa.Client{ShopID: "shop", SecretKey: "secret", BaseURL: srv.URL, HTTPClient: srv.Client()}
	return c
}

func nonNilProvider(pool *pgxpool.Pool) *yookassa.YooKassaProvider {
	return yookassa.NewProvider(pool, &yookassa.Client{ShopID: "shop", SecretKey: "secret"}, "https://console.dada-tuda.ru/billing/return", false)
}
