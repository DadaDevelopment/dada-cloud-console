package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/billing/yookassa"
	"github.com/dada-tuda/console/backend/internal/config"
)

func newPayCtx(method, path, body string, headers map[string]string, params gin.Params) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	c.Params = params
	return c, rec
}

func seedPayServiceKey(t *testing.T, pool *pgxpool.Pool, service string) (keyID uuid.UUID, plaintext string) {
	t.Helper()
	pt, hash, prefix, err := generatePayServiceKey()
	if err != nil {
		t.Fatalf("generatePayServiceKey: %v", err)
	}
	err = pool.QueryRow(context.Background(),
		`INSERT INTO pay_service_keys (service, token_hash, token_prefix) VALUES ($1, $2, $3) RETURNING id`,
		service, hash, prefix,
	).Scan(&keyID)
	if err != nil {
		t.Fatalf("seed pay service key: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM pay_service_keys WHERE id = $1`, keyID)
	})
	return keyID, pt
}

func seedServiceCharge(t *testing.T, pool *pgxpool.Pool, keyID uuid.UUID, service, externalRef, status, ykPaymentID string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO service_charges (service_key_id, service, external_ref, amount_value, currency, description, status, yk_payment_id)
		VALUES ($1, $2, $3, '500.00', 'RUB', 'test charge', $4, NULLIF($5, ''))
		RETURNING id
	`, keyID, service, externalRef, status, ykPaymentID).Scan(&id)
	if err != nil {
		t.Fatalf("seed service charge: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM service_charges WHERE id = $1`, id)
	})
	return id
}

// newCountingYooKassaClient returns a fake YooKassa client backed by an
// httptest.Server plus a counter of every HTTP call it received --
// TestCreateServiceCharge_IdempotentReplay_SameChargeOneYKCall relies on this
// to prove the idempotent replay never calls CreatePayment a second time.
func newCountingYooKassaClient(t *testing.T, status string) (*yookassa.Client, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(yookassa.Payment{
			ID:     "yk_pay_" + uuid.NewString()[:8],
			Status: status,
			Paid:   status == "succeeded",
			Amount: yookassa.Amount{Value: "500.00", Currency: "RUB"},
			Confirmation: yookassa.Confirmation{
				Type: "redirect",
				URL:  "https://yoomoney.ru/checkout/payments/v2/contract?orderId=fake",
			},
		})
	}))
	t.Cleanup(srv.Close)
	return &yookassa.Client{ShopID: "shop", SecretKey: "secret", BaseURL: srv.URL, HTTPClient: srv.Client()}, &calls
}

func testPayHandler(pool *pgxpool.Pool, client *yookassa.Client) *Handler {
	h := &Handler{pool: pool, cfg: &config.Config{YooKassaReturnURL: "https://console.dada-tuda.ru/pay/return"}}
	if client != nil {
		h.yookassa = yookassa.NewProvider(pool, client, h.cfg.YooKassaReturnURL, false)
	}
	return h
}

// --- key resolve ---

func TestGeneratePayServiceKey_FormatAndHash(t *testing.T) {
	plaintext, hash, prefix, err := generatePayServiceKey()
	if err != nil {
		t.Fatalf("generatePayServiceKey: %v", err)
	}
	if !strings.HasPrefix(plaintext, payKeyPrefix) {
		t.Fatalf("plaintext=%q want %q prefix", plaintext, payKeyPrefix)
	}
	if len(plaintext) != len(payKeyPrefix)+48 {
		t.Fatalf("plaintext len=%d want %d", len(plaintext), len(payKeyPrefix)+48)
	}
	if prefix != plaintext[:payKeyPrefixLen] {
		t.Fatalf("prefix=%q want first %d chars of plaintext", prefix, payKeyPrefixLen)
	}
	if hash != hashPayServiceKey(plaintext) {
		t.Fatalf("hash=%q does not match hashPayServiceKey(plaintext)", hash)
	}
}

func TestResolvePayServiceKey_Valid(t *testing.T) {
	pool := testPaymentsPool(t)
	_, plaintext := seedPayServiceKey(t, pool, "svc-valid-"+uuid.NewString()[:8])

	id, service, err := resolvePayServiceKey(context.Background(), pool, plaintext)
	if err != nil {
		t.Fatalf("resolvePayServiceKey: %v", err)
	}
	if id == uuid.Nil || service == "" {
		t.Fatalf("id=%v service=%q want both populated", id, service)
	}
}

func TestResolvePayServiceKey_Unknown(t *testing.T) {
	pool := testPaymentsPool(t)

	_, _, err := resolvePayServiceKey(context.Background(), pool, payKeyPrefix+"doesnotexist0000000000000000000000000000000000")
	if err != errPayKeyUnknown {
		t.Fatalf("err=%v want errPayKeyUnknown", err)
	}
}

func TestResolvePayServiceKey_Revoked(t *testing.T) {
	pool := testPaymentsPool(t)
	keyID, plaintext := seedPayServiceKey(t, pool, "svc-revoked-"+uuid.NewString()[:8])
	if _, err := pool.Exec(context.Background(), `UPDATE pay_service_keys SET revoked_at = now() WHERE id = $1`, keyID); err != nil {
		t.Fatalf("revoke key: %v", err)
	}

	_, _, err := resolvePayServiceKey(context.Background(), pool, plaintext)
	if err != errPayKeyUnknown {
		t.Fatalf("err=%v want errPayKeyUnknown for a revoked key", err)
	}
}

func TestResolvePayCaller_MissingHeader_401(t *testing.T) {
	h := &Handler{}
	c, rec := newPayCtx(http.MethodGet, "/api/v1/pay/charges", "", nil, nil)

	if _, _, ok := h.resolvePayCaller(c); ok {
		t.Fatal("resolvePayCaller returned ok=true with no auth header")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d want 401", rec.Code)
	}
}

func TestResolvePayCaller_DBError_503(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping pay-gateway DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	pool.Close()

	h := &Handler{pool: pool}
	c, rec := newPayCtx(http.MethodGet, "/api/v1/pay/charges", "", map[string]string{
		"Authorization": "Bearer " + payKeyPrefix + "0000000000000000000000000000000000000000000000",
	}, nil)

	if _, _, ok := h.resolvePayCaller(c); ok {
		t.Fatal("resolvePayCaller returned ok=true against a closed pool")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d body=%s want 503 for a DB outage on the auth path", rec.Code, rec.Body.String())
	}
}

// --- charge create ---

func TestCreateServiceCharge_HappyPath(t *testing.T) {
	pool := testPaymentsPool(t)
	_, plaintext := seedPayServiceKey(t, pool, "svc-happy-"+uuid.NewString()[:8])
	client, calls := newCountingYooKassaClient(t, "pending")
	h := testPayHandler(pool, client)

	body := `{"external_ref":"tg:1:plan-30d","amount":499.00,"description":"VPN 30 days"}`
	c, rec := newPayCtx(http.MethodPost, "/api/v1/pay/charges", body, map[string]string{"Authorization": "Bearer " + plaintext}, nil)
	h.CreateServiceCharge(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var resp serviceChargeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	if resp.Status != "pending" || resp.ConfirmationURL == "" || resp.Amount != "499.00" || resp.Currency != "RUB" {
		t.Fatalf("resp=%+v unexpected", resp)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("yookassa calls=%d want 1", atomic.LoadInt32(calls))
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM service_charges WHERE external_ref = 'tg:1:plan-30d'`)
	})
}

func TestCreateServiceCharge_IdempotentReplay_SameChargeOneYKCall(t *testing.T) {
	pool := testPaymentsPool(t)
	_, plaintext := seedPayServiceKey(t, pool, "svc-idem-"+uuid.NewString()[:8])
	client, calls := newCountingYooKassaClient(t, "pending")
	h := testPayHandler(pool, client)

	body := `{"external_ref":"tg:42:renew","amount":250.50,"description":"renew"}`
	headers := map[string]string{"Authorization": "Bearer " + plaintext}

	c1, rec1 := newPayCtx(http.MethodPost, "/api/v1/pay/charges", body, headers, nil)
	h.CreateServiceCharge(c1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("1st create code=%d body=%s want 200", rec1.Code, rec1.Body.String())
	}
	var first serviceChargeResponse
	if err := json.Unmarshal(rec1.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode 1st response: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM service_charges WHERE id = $1`, first.ID) })

	c2, rec2 := newPayCtx(http.MethodPost, "/api/v1/pay/charges", body, headers, nil)
	h.CreateServiceCharge(c2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("2nd create (replay) code=%d body=%s want 200", rec2.Code, rec2.Body.String())
	}
	var second serviceChargeResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode 2nd response: %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("replay returned a different charge: 1st=%s 2nd=%s", first.ID, second.ID)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("yookassa calls=%d want exactly 1 (replay must not create a 2nd payment)", atomic.LoadInt32(calls))
	}
}

// TestCreateServiceCharge_ReplayHealsUnlinkedCharge covers the crash window
// between the INSERT and the YooKassa call: the row exists and holds the
// caller's external_ref, so the UNIQUE idempotency guard will match it
// forever, but it has no payment behind it. Without healing the caller would
// get that dead row back on every retry and could never pay.
func TestCreateServiceCharge_ReplayHealsUnlinkedCharge(t *testing.T) {
	pool := testPaymentsPool(t)
	service := "svc-heal-" + uuid.NewString()[:8]
	keyID, plaintext := seedPayServiceKey(t, pool, service)
	chargeID := seedServiceCharge(t, pool, keyID, service, "tg:77:heal", "pending", "")

	client, calls := newCountingYooKassaClient(t, "pending")
	h := testPayHandler(pool, client)

	body := `{"external_ref":"tg:77:heal","amount":500.00,"description":"test charge"}`
	headers := map[string]string{"Authorization": "Bearer " + plaintext}
	c, rec := newPayCtx(http.MethodPost, "/api/v1/pay/charges", body, headers, nil)
	h.CreateServiceCharge(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("heal replay code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var got serviceChargeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ID != chargeID.String() {
		t.Fatalf("heal returned a different charge: got=%s want=%s", got.ID, chargeID)
	}
	if got.ConfirmationURL == "" {
		t.Fatal("healed charge still has no confirmation_url")
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("yookassa calls=%d want exactly 1", atomic.LoadInt32(calls))
	}

	var ykID string
	if err := pool.QueryRow(context.Background(),
		`SELECT coalesce(yk_payment_id, '') FROM service_charges WHERE id = $1`, chargeID).Scan(&ykID); err != nil {
		t.Fatalf("reload charge: %v", err)
	}
	if ykID == "" {
		t.Fatal("yk_payment_id was not persisted by the heal path")
	}

	c2, rec2 := newPayCtx(http.MethodPost, "/api/v1/pay/charges", body, headers, nil)
	h.CreateServiceCharge(c2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("post-heal replay code=%d want 200", rec2.Code)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("post-heal replay called yookassa again: calls=%d want 1", atomic.LoadInt32(calls))
	}
}

func TestCreateServiceCharge_AmountZeroOrNegative_400(t *testing.T) {
	pool := testPaymentsPool(t)
	_, plaintext := seedPayServiceKey(t, pool, "svc-neg-"+uuid.NewString()[:8])
	h := testPayHandler(pool, nil)

	for _, amount := range []string{"0", "-5.00"} {
		body := `{"external_ref":"tg:x","amount":` + amount + `,"description":"x"}`
		c, rec := newPayCtx(http.MethodPost, "/api/v1/pay/charges", body, map[string]string{"Authorization": "Bearer " + plaintext}, nil)
		h.CreateServiceCharge(c)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("amount=%s code=%d body=%s want 400", amount, rec.Code, rec.Body.String())
		}
	}
}

func TestCreateServiceCharge_AmountOverCeiling_400(t *testing.T) {
	pool := testPaymentsPool(t)
	_, plaintext := seedPayServiceKey(t, pool, "svc-ceil-"+uuid.NewString()[:8])
	h := testPayHandler(pool, nil)

	body := `{"external_ref":"tg:y","amount":100000.01,"description":"too much"}`
	c, rec := newPayCtx(http.MethodPost, "/api/v1/pay/charges", body, map[string]string{"Authorization": "Bearer " + plaintext}, nil)
	h.CreateServiceCharge(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s want 400 over the ceiling", rec.Code, rec.Body.String())
	}
}

func TestCreateServiceCharge_ProviderUnconfigured_409(t *testing.T) {
	pool := testPaymentsPool(t)
	_, plaintext := seedPayServiceKey(t, pool, "svc-unconf-"+uuid.NewString()[:8])
	h := testPayHandler(pool, nil)

	body := `{"external_ref":"tg:z","amount":10.00,"description":"x"}`
	c, rec := newPayCtx(http.MethodPost, "/api/v1/pay/charges", body, map[string]string{"Authorization": "Bearer " + plaintext}, nil)
	h.CreateServiceCharge(c)
	if rec.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s want 409 when payments are not configured", rec.Code, rec.Body.String())
	}
}

// --- cross-service isolation ---

func TestGetServiceCharge_CrossServiceIsolation_404(t *testing.T) {
	pool := testPaymentsPool(t)
	keyA, _ := seedPayServiceKey(t, pool, "svc-a-"+uuid.NewString()[:8])
	_, plaintextB := seedPayServiceKey(t, pool, "svc-b-"+uuid.NewString()[:8])
	chargeID := seedServiceCharge(t, pool, keyA, "svc-a", "ref-a", "pending", "")

	h := testPayHandler(pool, nil)
	c, rec := newPayCtx(http.MethodGet, "/api/v1/pay/charges/"+chargeID.String(), "", map[string]string{"Authorization": "Bearer " + plaintextB}, gin.Params{{Key: "chargeId", Value: chargeID.String()}})
	h.GetServiceCharge(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s want 404: service B must not see service A's charge", rec.Code, rec.Body.String())
	}
}

// --- reconcile on read ---

func TestGetServiceCharge_ReconcileOnRead_FlipsOnceThenNoop(t *testing.T) {
	pool := testPaymentsPool(t)
	keyID, plaintext := seedPayServiceKey(t, pool, "svc-recon-"+uuid.NewString()[:8])
	ykID := "ykid-recon-" + uuid.NewString()[:8]
	chargeID := seedServiceCharge(t, pool, keyID, "svc-recon", "ref-recon", "pending", ykID)

	client, calls := newCountingYooKassaClient(t, "succeeded")
	h := testPayHandler(pool, client)

	c1, rec1 := newPayCtx(http.MethodGet, "/api/v1/pay/charges/"+chargeID.String(), "", map[string]string{"Authorization": "Bearer " + plaintext}, gin.Params{{Key: "chargeId", Value: chargeID.String()}})
	h.GetServiceCharge(c1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("1st read code=%d body=%s want 200", rec1.Code, rec1.Body.String())
	}
	var first serviceChargeResponse
	if err := json.Unmarshal(rec1.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode 1st response: %v", err)
	}
	if first.Status != "succeeded" || first.PaidAt == nil {
		t.Fatalf("1st read status=%q paidAt=%v want succeeded+paid_at set", first.Status, first.PaidAt)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("yookassa calls after 1st read=%d want 1", atomic.LoadInt32(calls))
	}

	c2, rec2 := newPayCtx(http.MethodGet, "/api/v1/pay/charges/"+chargeID.String(), "", map[string]string{"Authorization": "Bearer " + plaintext}, gin.Params{{Key: "chargeId", Value: chargeID.String()}})
	h.GetServiceCharge(c2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("2nd read code=%d body=%s want 200", rec2.Code, rec2.Body.String())
	}
	var second serviceChargeResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode 2nd response: %v", err)
	}
	if second.Status != "succeeded" {
		t.Fatalf("2nd read status=%q want still succeeded", second.Status)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("yookassa calls after 2nd (already-terminal) read=%d want still 1 (no re-fetch)", atomic.LoadInt32(calls))
	}
}

// --- webhook routing ---

func TestYooKassaWebhook_ServiceCharge_FlipsUnknownToPlansID(t *testing.T) {
	pool := testPaymentsPool(t)
	keyID, _ := seedPayServiceKey(t, pool, "svc-wh-"+uuid.NewString()[:8])
	ykID := "ykid-wh-" + uuid.NewString()[:8]
	chargeID := seedServiceCharge(t, pool, keyID, "svc-wh", "ref-wh", "pending", ykID)

	client, _ := newCountingYooKassaClient(t, "succeeded")
	h := testPayHandler(pool, client)

	c, rec := newWebhookCtx(t, "", `{"event":"payment.succeeded","object":{"id":"`+ykID+`"}}`)
	h.YooKassaWebhook(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM service_charges WHERE id = $1`, chargeID).Scan(&status); err != nil {
		t.Fatalf("read charge status: %v", err)
	}
	if status != "succeeded" {
		t.Fatalf("charge status=%q want succeeded after webhook for an id unknown to the plans table", status)
	}
}

func TestYooKassaWebhook_ServiceCharge_ReplayIsIdempotent(t *testing.T) {
	pool := testPaymentsPool(t)
	keyID, _ := seedPayServiceKey(t, pool, "svc-wh2-"+uuid.NewString()[:8])
	ykID := "ykid-wh2-" + uuid.NewString()[:8]
	chargeID := seedServiceCharge(t, pool, keyID, "svc-wh2", "ref-wh2", "pending", ykID)

	client, calls := newCountingYooKassaClient(t, "succeeded")
	h := testPayHandler(pool, client)

	body := `{"event":"payment.succeeded","object":{"id":"` + ykID + `"}}`
	c1, rec1 := newWebhookCtx(t, "", body)
	h.YooKassaWebhook(c1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("1st delivery code=%d want 200", rec1.Code)
	}
	callsAfterFirst := atomic.LoadInt32(calls)

	c2, rec2 := newWebhookCtx(t, "", body)
	h.YooKassaWebhook(c2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("2nd delivery code=%d want 200", rec2.Code)
	}
	callsAfterSecond := atomic.LoadInt32(calls)

	var status string
	var paidAtSet bool
	if err := pool.QueryRow(context.Background(), `SELECT status, paid_at IS NOT NULL FROM service_charges WHERE id = $1`, chargeID).Scan(&status, &paidAtSet); err != nil {
		t.Fatalf("read charge: %v", err)
	}
	if status != "succeeded" || !paidAtSet {
		t.Fatalf("status=%q paidAtSet=%v want succeeded+paid_at set after replay", status, paidAtSet)
	}
	if delta := callsAfterSecond - callsAfterFirst; delta != 1 {
		t.Fatalf("GetPayment calls added by 2nd delivery=%d want 1 (ProcessWebhook's mandatory re-fetch only; processServiceChargeWebhook must add 0 on an already_processed replay)", delta)
	}
}

func TestYooKassaWebhook_UnknownToBothPlansAndServiceCharges_Returns200(t *testing.T) {
	pool := testPaymentsPool(t)
	client, _ := newCountingYooKassaClient(t, "succeeded")
	h := testPayHandler(pool, client)

	unknownID := "ykid-nowhere-" + uuid.NewString()[:8]
	c, rec := newWebhookCtx(t, "", `{"event":"payment.succeeded","object":{"id":"`+unknownID+`"}}`)
	h.YooKassaWebhook(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200 even when the id is unknown to both tables (must never make YooKassa retry forever)", rec.Code, rec.Body.String())
	}
}
