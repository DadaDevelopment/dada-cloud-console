package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/billing/yookassa"
)

// payKeyPrefix marks a payment-gateway service key plaintext. This is a
// separate namespace from aiKeyPrefix (sk-dada-ai-) and from app deploy-hook
// tokens: the gateway phase 1 is deliberately not multi-tenant ("gejtvej
// tolko dlja nashih servisov" -- owner decision) and its keys are minted by a
// platform admin for an internal service, not self-service by a project
// member.
const payKeyPrefix = "sk-dada-pay-"

// payKeyPrefixLen is how many leading plaintext characters are persisted as
// token_prefix: payKeyPrefix plus 6 hex chars, enough to tell two keys apart
// in a list without revealing either.
const payKeyPrefixLen = len(payKeyPrefix) + 6

// payKeyUsageThrottleSQL bounds how often a key's last_used_at is refreshed,
// mirroring aiKeyUsageThrottleSQL: a bot polling its own charge status every
// few seconds must not turn this into an UPDATE-per-request table.
const payKeyUsageThrottleSQL = "5 minutes"

// maxServiceChargeAmount is the per-charge ceiling in RUB. The amount is
// caller-supplied (unlike a plan, which is always server-priced) because the
// caller here is one of OUR OWN services authenticated by a revocable key,
// not an end customer -- that trust boundary is what makes accepting a
// caller-supplied amount acceptable at all, and the ceiling bounds the blast
// radius of a compromised or buggy service key.
const maxServiceChargeAmount = 100000.00

// errPayKeyUnknown marks a pay-gateway key that does not resolve to an active
// row: either it was never minted or it has been revoked. Both cases answer
// the same 401 to the caller -- the taxonomy split that matters is this vs. a
// genuine backend outage (503), not "unknown" vs. "revoked".
var errPayKeyUnknown = errors.New("pay: unknown or revoked service key")

// payScopeCharge is the scope a ServiceIdentity must carry to spend money
// (ADR-021 phase 3). It is deliberately absent from identityDefaultScopes: an
// app gets AI for free because every app infers, but an app that can charge a
// customer has to say so at mint time.
const payScopeCharge = "pay:charge"

// generatePayServiceKey mints a new plaintext pay-gateway key plus its
// derived hash and prefix, mirroring generateAIKey exactly. The plaintext is
// payKeyPrefix followed by 48 hex characters (24 random bytes) and is
// returned to the caller exactly once, at mint time.
func generatePayServiceKey() (plaintext, hash, prefix string, err error) {
	buf := make([]byte, 24)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", err
	}
	plaintext = payKeyPrefix + hex.EncodeToString(buf)
	hash = hashPayServiceKey(plaintext)
	prefix = plaintext[:payKeyPrefixLen]
	return plaintext, hash, prefix, nil
}

// hashPayServiceKey returns the hex-encoded sha256 of a pay-gateway key
// plaintext -- the only form ever persisted or compared against.
func hashPayServiceKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// resolvePayServiceKey resolves a plaintext pay-gateway key to its owning
// service. Returns errPayKeyUnknown when the key does not match any active
// row (unknown or revoked); any other error is a genuine backend failure the
// caller must answer with 503, not 401 -- a Postgres outage on the auth path
// must never masquerade as a flood of bad-key rejections.
func resolvePayServiceKey(ctx context.Context, pool *pgxpool.Pool, token string) (keyID uuid.UUID, service string, err error) {
	err = pool.QueryRow(ctx,
		`SELECT id, service FROM pay_service_keys WHERE token_hash = $1 AND revoked_at IS NULL`,
		hashPayServiceKey(token),
	).Scan(&keyID, &service)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", errPayKeyUnknown
	}
	if err != nil {
		return uuid.Nil, "", err
	}
	return keyID, service, nil
}

// touchPayServiceKey refreshes last_used_at at most once per
// payKeyUsageThrottleSQL. Best effort: a failed bookkeeping write must never
// fail the request that is holding it up.
func (h *Handler) touchPayServiceKey(ctx context.Context, keyID uuid.UUID) {
	_, _ = h.pool.Exec(ctx,
		`UPDATE pay_service_keys SET last_used_at = now()
		  WHERE id = $1
		    AND (last_used_at IS NULL OR last_used_at < now() - $2::interval)`,
		keyID, payKeyUsageThrottleSQL,
	)
}

// extractPayServiceKey reads the bearer token from either Authorization:
// Bearer <key> or X-API-Key, mirroring how the deploy-hook and gateway
// server accept credentials from a caller that may not be a browser.
func extractPayServiceKey(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); h != "" {
		if strings.HasPrefix(h, "Bearer ") {
			return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
	}
	return strings.TrimSpace(c.GetHeader("X-API-Key"))
}

// payCaller is whoever a pay-gateway request authenticated as: either a
// phase-1 pay_service_keys row or a ServiceIdentity holding pay:charge.
// Exactly one of the two ids is set, which is the same invariant migration
// 089 enforces on the row the caller goes on to own.
type payCaller struct {
	KeyID      *uuid.UUID
	IdentityID *uuid.UUID
	Service    string
}

// ownerColumn names the service_charges column that scopes this caller's
// rows. The value comes from a closed set of two literals, never from a
// request, so interpolating it into SQL adds no injection surface.
func (pc payCaller) ownerColumn() string {
	if pc.IdentityID != nil {
		return "identity_id"
	}
	return "service_key_id"
}

// ownerID is the value ownerColumn is compared against.
func (pc payCaller) ownerID() uuid.UUID {
	if pc.IdentityID != nil {
		return *pc.IdentityID
	}
	if pc.KeyID != nil {
		return *pc.KeyID
	}
	return uuid.Nil
}

// resolvePayCaller authenticates a pay-gateway request from either credential
// family, told apart by prefix alone. On failure it writes the response itself
// (401 for a missing/unknown/revoked credential, 403 for an identity without
// pay:charge, 503 for a backend failure) and returns ok=false; callers must
// return immediately.
//
// An identity that resolves but lacks the scope is 403, not 401: the
// credential is genuine and the caller should stop retrying and re-mint with
// the scope, which a 401 would not tell it.
func (h *Handler) resolvePayCaller(c *gin.Context) (pc payCaller, ok bool) {
	token := extractPayServiceKey(c)
	if token == "" {
		respondError(c, http.StatusUnauthorized, "invalid api key")
		return payCaller{}, false
	}
	if strings.HasPrefix(token, identityTokenPrefix) {
		return h.resolvePayIdentity(c, token)
	}
	id, svc, err := resolvePayServiceKey(c.Request.Context(), h.pool, token)
	if err != nil {
		if errors.Is(err, errPayKeyUnknown) {
			respondError(c, http.StatusUnauthorized, "invalid api key")
			return payCaller{}, false
		}
		log.Printf("pay: key resolution failed: %v", err)
		respondError(c, http.StatusServiceUnavailable, "auth backend unavailable")
		return payCaller{}, false
	}
	h.touchPayServiceKey(c.Request.Context(), id)
	return payCaller{KeyID: &id, Service: svc}, true
}

// resolvePayIdentity is the sk-dada-id- half of resolvePayCaller.
func (h *Handler) resolvePayIdentity(c *gin.Context, token string) (payCaller, bool) {
	ctx := c.Request.Context()
	ident, err := resolveIdentityToken(ctx, h.pool, token)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(c, http.StatusUnauthorized, "invalid api key")
		return payCaller{}, false
	}
	if err != nil {
		log.Printf("pay: identity resolution failed: %v", err)
		respondError(c, http.StatusServiceUnavailable, "auth backend unavailable")
		return payCaller{}, false
	}
	if !identityHasScope(ident.Scopes, payScopeCharge) {
		respondError(c, http.StatusForbidden, "identity is not granted the "+payScopeCharge+" scope")
		return payCaller{}, false
	}
	h.touchIdentityToken(ctx, ident.TokenID)
	service := ident.AppName
	if service == "" {
		service = "identity:" + ident.IdentityID.String()
	}
	identityID := ident.IdentityID
	return payCaller{IdentityID: &identityID, Service: service}, true
}

// serviceCharge is one service_charges row.
type serviceCharge struct {
	ID              uuid.UUID
	ServiceKeyID    *uuid.UUID
	IdentityID      *uuid.UUID
	Service         string
	ExternalRef     string
	AmountValue     string
	Currency        string
	Description     string
	Status          string
	YKPaymentID     string
	ConfirmationURL string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	PaidAt          *time.Time
}

// serviceChargeResponse is the wire shape returned by every pay/charges
// endpoint.
type serviceChargeResponse struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	ConfirmationURL string     `json:"confirmation_url,omitempty"`
	Amount          string     `json:"amount"`
	Currency        string     `json:"currency"`
	ExternalRef     string     `json:"external_ref"`
	Description     string     `json:"description"`
	CreatedAt       time.Time  `json:"created_at"`
	PaidAt          *time.Time `json:"paid_at,omitempty"`
}

func toChargeResponse(sc serviceCharge) serviceChargeResponse {
	return serviceChargeResponse{
		ID:              sc.ID.String(),
		Status:          sc.Status,
		ConfirmationURL: sc.ConfirmationURL,
		Amount:          sc.AmountValue,
		Currency:        sc.Currency,
		ExternalRef:     sc.ExternalRef,
		Description:     sc.Description,
		CreatedAt:       sc.CreatedAt,
		PaidAt:          sc.PaidAt,
	}
}

const serviceChargeColumns = `id, service_key_id, identity_id, service, external_ref, amount_value::text, currency, description, status, coalesce(yk_payment_id, ''), coalesce(confirmation_url, ''), created_at, updated_at, paid_at`

func scanServiceCharge(row pgx.Row) (serviceCharge, error) {
	var sc serviceCharge
	err := row.Scan(&sc.ID, &sc.ServiceKeyID, &sc.IdentityID, &sc.Service, &sc.ExternalRef, &sc.AmountValue, &sc.Currency,
		&sc.Description, &sc.Status, &sc.YKPaymentID, &sc.ConfirmationURL, &sc.CreatedAt, &sc.UpdatedAt, &sc.PaidAt)
	return sc, err
}

// loadServiceCharge loads a charge scoped to the caller. Scoping the WHERE
// clause by the owner column (not just id) is the cross-service isolation
// guard: a charge that belongs to another service answers pgx.ErrNoRows here,
// which callers map to 404 -- never leaking that the charge id exists at all.
func (h *Handler) loadServiceCharge(ctx context.Context, chargeID uuid.UUID, pc payCaller) (serviceCharge, error) {
	row := h.pool.QueryRow(ctx,
		`SELECT `+serviceChargeColumns+` FROM service_charges WHERE id = $1 AND `+pc.ownerColumn()+` = $2`,
		chargeID, pc.ownerID(),
	)
	return scanServiceCharge(row)
}

// loadServiceChargeByRef loads a charge by the caller's own idempotency key,
// (owner, external_ref) -- the pair a UNIQUE constraint enforces on both
// halves: 083's for a service key, 089's partial index for an identity.
func (h *Handler) loadServiceChargeByRef(ctx context.Context, pc payCaller, externalRef string) (serviceCharge, error) {
	row := h.pool.QueryRow(ctx,
		`SELECT `+serviceChargeColumns+` FROM service_charges WHERE `+pc.ownerColumn()+` = $1 AND external_ref = $2`,
		pc.ownerID(), externalRef,
	)
	return scanServiceCharge(row)
}

// reconcileServiceCharge re-fetches the authoritative YooKassa status for a
// pending charge and flips the row if it has actually settled. The FOR
// UPDATE row lock plus the re-check of status == "pending" AFTER acquiring
// it is the idempotence guard: it mirrors provider.go's ProcessWebhook, so a
// concurrent webhook delivery and a concurrent poll can never both apply the
// same transition twice. Never trusts cached state -- this always re-asks
// YooKassa before answering "succeeded".
func (h *Handler) reconcileServiceCharge(ctx context.Context, chargeID uuid.UUID, pc payCaller) (serviceCharge, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return serviceCharge{}, fmt.Errorf("pay: begin reconcile tx: %w", err)
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx,
		`SELECT `+serviceChargeColumns+` FROM service_charges WHERE id = $1 AND `+pc.ownerColumn()+` = $2 FOR UPDATE`,
		chargeID, pc.ownerID(),
	)
	sc, err := scanServiceCharge(row)
	if err != nil {
		return serviceCharge{}, err
	}
	if sc.Status != "pending" || sc.YKPaymentID == "" || h.yookassa == nil {
		return sc, nil
	}

	payment, err := h.yookassa.Client.GetPayment(ctx, sc.YKPaymentID)
	if err != nil {
		return serviceCharge{}, fmt.Errorf("pay: refetch payment %s: %w", sc.YKPaymentID, err)
	}

	now := time.Now().UTC()
	switch payment.Status {
	case "succeeded":
		if _, err := tx.Exec(ctx,
			`UPDATE service_charges SET status = 'succeeded', paid_at = $1, updated_at = $1 WHERE id = $2`,
			now, sc.ID,
		); err != nil {
			return serviceCharge{}, fmt.Errorf("pay: mark succeeded: %w", err)
		}
		sc.Status, sc.PaidAt, sc.UpdatedAt = "succeeded", &now, now
	case "canceled":
		if _, err := tx.Exec(ctx,
			`UPDATE service_charges SET status = 'canceled', updated_at = $1 WHERE id = $2`,
			now, sc.ID,
		); err != nil {
			return serviceCharge{}, fmt.Errorf("pay: mark canceled: %w", err)
		}
		sc.Status, sc.UpdatedAt = "canceled", now
	default:
		return sc, nil
	}

	if err := tx.Commit(ctx); err != nil {
		return serviceCharge{}, fmt.Errorf("pay: commit reconcile: %w", err)
	}
	return sc, nil
}

// processServiceChargeWebhook is the service_charges half of the single
// shop-wide YooKassa webhook, invoked from YooKassaWebhook (billing_payments.go)
// when the plan-payments processor answers OutcomeUnknownPayment. ykPaymentID
// is looked up ONLY to find which local row to re-fetch and lock -- the
// webhook payload's claimed status is still never trusted; GetPayment is the
// sole source of truth, same as ProcessWebhook and reconcileServiceCharge.
func (h *Handler) processServiceChargeWebhook(ctx context.Context, ykPaymentID string) (outcome string, err error) {
	if h.yookassa == nil {
		return "unconfigured", nil
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("pay: begin webhook tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var id uuid.UUID
	var status string
	err = tx.QueryRow(ctx,
		`SELECT id, status FROM service_charges WHERE yk_payment_id = $1 FOR UPDATE`,
		ykPaymentID,
	).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "unknown_charge", nil
	}
	if err != nil {
		return "", fmt.Errorf("pay: lookup charge row: %w", err)
	}
	if status != "pending" {
		return "already_processed", nil
	}

	payment, err := h.yookassa.Client.GetPayment(ctx, ykPaymentID)
	if err != nil {
		return "", fmt.Errorf("pay: refetch payment %s: %w", ykPaymentID, err)
	}

	now := time.Now().UTC()
	switch payment.Status {
	case "succeeded":
		if _, err := tx.Exec(ctx,
			`UPDATE service_charges SET status = 'succeeded', paid_at = $1, updated_at = $1 WHERE id = $2`,
			now, id,
		); err != nil {
			return "", fmt.Errorf("pay: mark succeeded: %w", err)
		}
	case "canceled":
		if _, err := tx.Exec(ctx,
			`UPDATE service_charges SET status = 'canceled', updated_at = $1 WHERE id = $2`,
			now, id,
		); err != nil {
			return "", fmt.Errorf("pay: mark canceled: %w", err)
		}
	default:
		return "noop", nil
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("pay: commit webhook: %w", err)
	}
	return payment.Status, nil
}

// createChargeRequest is the body of POST /api/v1/pay/charges.
// linkServiceChargePayment creates the YooKassa payment for an already-stored
// pending charge and writes the resulting id and confirmation URL back onto
// the row.
//
// It is deliberately callable twice for the same charge. If the process dies
// (or YooKassa times out) between the INSERT and this call, the row is left
// pending with no yk_payment_id, and the caller's next retry with the same
// external_ref lands here again to heal it -- otherwise the UNIQUE
// (service_key_id, external_ref) idempotency guard would hand that caller the
// same dead row with no confirmation URL forever, with no way to ever pay.
// Re-running it is safe because the Idempotence-Key is the charge UUID, so
// YooKassa returns the same payment rather than creating a second one.
func (h *Handler) linkServiceChargePayment(ctx context.Context, sc serviceCharge, returnURL string) error {
	if returnURL == "" {
		returnURL = h.cfg.YooKassaReturnURL
	}
	payment, err := h.yookassa.Client.CreatePayment(ctx, sc.ID.String(), yookassa.CreatePaymentRequest{
		Amount:       yookassa.Amount{Value: sc.AmountValue, Currency: sc.Currency},
		Capture:      true,
		Confirmation: &yookassa.Confirmation{Type: "redirect", ReturnURL: returnURL},
		Description:  sc.Description,
		Metadata: map[string]any{
			"kind":      "service_charge",
			"service":   sc.Service,
			"charge_id": sc.ID.String(),
		},
	})
	if err != nil {
		return fmt.Errorf("pay: create yookassa payment: %w", err)
	}
	if _, err := h.pool.Exec(ctx,
		`UPDATE service_charges SET yk_payment_id = $1, confirmation_url = $2, updated_at = now() WHERE id = $3`,
		payment.ID, payment.Confirmation.URL, sc.ID,
	); err != nil {
		return fmt.Errorf("pay: store payment reference: %w", err)
	}
	return nil
}

type createChargeRequest struct {
	ExternalRef string         `json:"external_ref"`
	Amount      float64        `json:"amount"`
	Currency    string         `json:"currency"`
	Description string         `json:"description"`
	ReturnURL   string         `json:"return_url"`
	Metadata    map[string]any `json:"metadata"`
}

// CreateServiceCharge creates (or, for a replayed external_ref, returns the
// existing) YooKassa payment for an internal service. Key-authenticated
// inside the handler -- see resolvePayCaller -- not the console JWT
// middleware; the caller is a bare-VPS Telegram bot with no console session.
//
// Idempotent by design: (owner, external_ref) is UNIQUE -- 083's constraint
// for a service key, 089's partial index for an identity -- so a
// caller that retries a create (e.g. after a timeout) with the same ref gets
// back the SAME charge and its existing confirmation_url instead of a second
// YooKassa payment -- this is the whole point for a bot with no reliable
// at-most-once delivery of its own.
//
// @ID          createServiceCharge
// @Summary     Create an internal-service payment charge
// @Description Internal payment gateway: lets one of our own credential-authenticated services (a pay service key, or a ServiceIdentity holding pay:charge) create a YooKassa payment and poll its status. Idempotent on external_ref.
// @Tags        pay-gateway
// @Accept      json
// @Produce     json
// @Param       body body     createChargeRequest true "Charge request"
// @Success     200  {object} serviceChargeResponse
// @Failure     400  {object} map[string]string
// @Failure     401  {object} map[string]string
// @Failure     403  {object} map[string]string
// @Failure     409  {object} map[string]string
// @Failure     503  {object} map[string]string
// @Router      /pay/charges [post]
func (h *Handler) CreateServiceCharge(c *gin.Context) {
	pc, ok := h.resolvePayCaller(c)
	if !ok {
		return
	}
	service := pc.Service

	var req createChargeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	req.ExternalRef = strings.TrimSpace(req.ExternalRef)
	req.Description = strings.TrimSpace(req.Description)
	if req.ExternalRef == "" {
		respondError(c, http.StatusBadRequest, "external_ref is required")
		return
	}
	if req.Description == "" {
		respondError(c, http.StatusBadRequest, "description is required")
		return
	}
	if req.Amount <= 0 {
		respondError(c, http.StatusBadRequest, "amount must be greater than 0")
		return
	}
	if req.Amount > maxServiceChargeAmount {
		respondError(c, http.StatusBadRequest, fmt.Sprintf("amount exceeds the per-charge ceiling of %.2f", maxServiceChargeAmount))
		return
	}
	currency := strings.TrimSpace(req.Currency)
	if currency == "" {
		currency = "RUB"
	}

	if h.yookassa == nil {
		respondError(c, http.StatusConflict, "payments_not_configured")
		return
	}

	ctx := c.Request.Context()

	if existing, err := h.loadServiceChargeByRef(ctx, pc, req.ExternalRef); err == nil {
		if existing.YKPaymentID != "" || existing.Status != "pending" {
			c.JSON(http.StatusOK, toChargeResponse(existing))
			return
		}
		if err := h.linkServiceChargePayment(ctx, existing, req.ReturnURL); err != nil {
			log.Printf("pay: heal unlinked charge failed service=%s charge=%s: %v", service, existing.ID, err)
			respondError(c, http.StatusInternalServerError, "failed to create payment")
			return
		}
		healed, err := h.loadServiceCharge(ctx, existing.ID, pc)
		if err != nil {
			respondError(c, http.StatusInternalServerError, "failed to load charge")
			return
		}
		c.JSON(http.StatusOK, toChargeResponse(healed))
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		respondError(c, http.StatusInternalServerError, "failed to check existing charge")
		return
	}

	id := uuid.New()
	amountValue := fmt.Sprintf("%.2f", req.Amount)
	metadataJSON, err := json.Marshal(req.Metadata)
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid metadata")
		return
	}
	if req.Metadata == nil {
		metadataJSON = []byte("{}")
	}

	_, err = h.pool.Exec(ctx, `
		INSERT INTO service_charges (id, service_key_id, identity_id, service, external_ref, amount_value, currency, description, status, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', $9)
	`, id, pc.KeyID, pc.IdentityID, service, req.ExternalRef, amountValue, currency, req.Description, metadataJSON)
	if err != nil {
		if isUniqueViolation(err) {
			if existing, err2 := h.loadServiceChargeByRef(ctx, pc, req.ExternalRef); err2 == nil {
				c.JSON(http.StatusOK, toChargeResponse(existing))
				return
			}
		}
		respondError(c, http.StatusInternalServerError, "failed to create charge")
		return
	}

	pending := serviceCharge{
		ID:          id,
		Service:     service,
		AmountValue: amountValue,
		Currency:    currency,
		Description: req.Description,
	}
	if err := h.linkServiceChargePayment(ctx, pending, req.ReturnURL); err != nil {
		log.Printf("pay: create payment failed service=%s charge=%s: %v", service, id, err)
		respondError(c, http.StatusInternalServerError, "failed to create payment")
		return
	}
	created, err := h.loadServiceCharge(ctx, id, pc)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load charge")
		return
	}

	h.recordSystemAudit(ctx, auditEntry{
		Action:       "ServiceChargeCreated",
		ResourceKind: "ServiceCharge",
		ResourceName: id.String(),
		Outcome:      auditOutcomeSuccess,
		Metadata: map[string]string{
			"service":       service,
			"owner":         pc.ownerColumn() + "=" + pc.ownerID().String(),
			"external_ref":  req.ExternalRef,
			"amount_value":  amountValue,
			"yk_payment_id": created.YKPaymentID,
		},
	})

	c.JSON(http.StatusOK, toChargeResponse(created))
}

// GetServiceCharge returns the status of one charge, scoped to the calling
// credential -- a charge belonging to another service answers 404, not 403, so a
// probing caller cannot even learn that the id exists. This is the MVP
// delivery mechanism for a caller with no inbound HTTP endpoint of its own
// (the VPN bot on a bare VPS): it polls this endpoint instead of receiving a
// callback. A pending charge with a stored yk_payment_id is reconciled
// against YooKassa on every read (see reconcileServiceCharge) rather than
// trusting whatever the row last said.
//
// @ID          getServiceCharge
// @Summary     Get an internal-service charge
// @Description Returns one charge's current status, reconciling against YooKassa first if it is still pending. 404 if the charge does not belong to the calling key.
// @Tags        pay-gateway
// @Produce     json
// @Param       chargeId path     string true "Charge UUID"
// @Success     200      {object} serviceChargeResponse
// @Failure     401      {object} map[string]string
// @Failure     403      {object} map[string]string
// @Failure     404      {object} map[string]string
// @Router      /pay/charges/{chargeId} [get]
func (h *Handler) GetServiceCharge(c *gin.Context) {
	pc, ok := h.resolvePayCaller(c)
	if !ok {
		return
	}
	chargeID, err := uuid.Parse(c.Param("chargeId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	ctx := c.Request.Context()
	sc, err := h.loadServiceCharge(ctx, chargeID, pc)
	if errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load charge")
		return
	}

	if sc.Status == "pending" && sc.YKPaymentID != "" {
		if reconciled, err := h.reconcileServiceCharge(ctx, chargeID, pc); err != nil {
			log.Printf("pay: reconcile charge %s failed: %v", chargeID, err)
		} else {
			sc = reconciled
		}
	}

	c.JSON(http.StatusOK, toChargeResponse(sc))
}

// ListServiceCharges lists the calling service's own charges, newest first.
// Never spans services -- there is no query parameter that widens this past
// the calling credential's own rows.
//
// @ID          listServiceCharges
// @Summary     List an internal service's charges
// @Description Lists charges created by the calling service key, newest first.
// @Tags        pay-gateway
// @Produce     json
// @Param       limit  query    int    false "Max rows (default 50, max 200)"
// @Param       status query    string false "Filter by status (pending|succeeded|canceled)"
// @Success     200    {object} map[string]interface{}
// @Failure     401    {object} map[string]string
// @Failure     403    {object} map[string]string
// @Router      /pay/charges [get]
func (h *Handler) ListServiceCharges(c *gin.Context) {
	pc, ok := h.resolvePayCaller(c)
	if !ok {
		return
	}

	limit := 50
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	status := strings.TrimSpace(c.Query("status"))

	ctx := c.Request.Context()
	var rows pgx.Rows
	var err error
	if status != "" {
		rows, err = h.pool.Query(ctx,
			`SELECT `+serviceChargeColumns+` FROM service_charges WHERE `+pc.ownerColumn()+` = $1 AND status = $2 ORDER BY created_at DESC LIMIT $3`,
			pc.ownerID(), status, limit,
		)
	} else {
		rows, err = h.pool.Query(ctx,
			`SELECT `+serviceChargeColumns+` FROM service_charges WHERE `+pc.ownerColumn()+` = $1 ORDER BY created_at DESC LIMIT $2`,
			pc.ownerID(), limit,
		)
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list charges")
		return
	}
	defer rows.Close()

	out := []serviceChargeResponse{}
	for rows.Next() {
		sc, err := scanServiceCharge(rows)
		if err != nil {
			respondError(c, http.StatusInternalServerError, "failed to read charge")
			return
		}
		out = append(out, toChargeResponse(sc))
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read charges")
		return
	}

	c.JSON(http.StatusOK, gin.H{"charges": out})
}

// createPayServiceKeyRequest is the body of POST /api/v1/admin/pay/keys.
type createPayServiceKeyRequest struct {
	Service string `json:"service" binding:"required"`
}

// createPayServiceKeyResponse carries the plaintext key exactly once, the
// same contract as createAIKeyResponse.
type createPayServiceKeyResponse struct {
	ID          uuid.UUID `json:"id"`
	Service     string    `json:"service"`
	Key         string    `json:"key"`
	TokenPrefix string    `json:"token_prefix"`
	CreatedAt   time.Time `json:"created_at"`
}

type payServiceKeyListItem struct {
	ID          uuid.UUID  `json:"id"`
	Service     string     `json:"service"`
	TokenPrefix string     `json:"token_prefix"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

// CreatePayServiceKey mints a credential for one internal service.
// Platform-admin only (/platform-admins group) -- this is a staff operation:
// phase 1 has no self-service path, since the whole point is that only OUR
// OWN services get a key, not a project member's own workload.
//
// @ID          createPayServiceKey
// @Summary     Mint an internal payment-gateway service key (platform-admin only)
// @Description Mints a revocable key for one internal service (e.g. dada-vpn-bot). The plaintext key is returned ONLY in this response. Platform-admin only.
// @Tags        pay-gateway-admin
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body     createPayServiceKeyRequest true "Service slug"
// @Success     201  {object} createPayServiceKeyResponse
// @Failure     400  {object} map[string]string
// @Failure     403  {object} map[string]string
// @Failure     409  {object} map[string]string
// @Router      /admin/pay/keys [post]
func (h *Handler) CreatePayServiceKey(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isGod(claims) {
		respondForbidden(c)
		return
	}

	var req createPayServiceKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	service := strings.TrimSpace(req.Service)
	if service == "" {
		respondError(c, http.StatusBadRequest, "service is required")
		return
	}

	plaintext, hash, prefix, err := generatePayServiceKey()
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to generate key")
		return
	}

	var id uuid.UUID
	var createdAt time.Time
	err = h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO pay_service_keys (service, token_hash, token_prefix, created_by)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, created_at`,
		service, hash, prefix, claims.UserID,
	).Scan(&id, &createdAt)
	if err != nil {
		if isUniqueViolation(err) {
			respondError(c, http.StatusConflict, "service already has a key")
			return
		}
		respondError(c, http.StatusInternalServerError, "failed to create service key")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		Action:       "CreatePayServiceKey",
		ResourceKind: "PayServiceKey",
		ResourceName: service,
		Outcome:      auditOutcomeSuccess,
		Metadata:     map[string]any{"service": service, "token_prefix": prefix},
	})

	c.JSON(http.StatusCreated, createPayServiceKeyResponse{
		ID:          id,
		Service:     service,
		Key:         plaintext,
		TokenPrefix: prefix,
		CreatedAt:   createdAt,
	})
}

// ListPayServiceKeys lists every pay-gateway service key, revoked ones
// included. Platform-admin only. Never returns the plaintext key or its hash.
//
// @ID          listPayServiceKeys
// @Summary     List internal payment-gateway service keys (platform-admin only)
// @Description Lists every service key minted for the payment gateway, including revoked ones. Platform-admin only.
// @Tags        pay-gateway-admin
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]interface{}
// @Failure     403 {object} map[string]string
// @Router      /admin/pay/keys [get]
func (h *Handler) ListPayServiceKeys(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isGod(claims) {
		respondForbidden(c)
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, service, token_prefix, created_at, last_used_at, revoked_at
		   FROM pay_service_keys
		  ORDER BY created_at DESC`,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query service keys")
		return
	}
	defer rows.Close()

	out := []payServiceKeyListItem{}
	for rows.Next() {
		var it payServiceKeyListItem
		if err := rows.Scan(&it.ID, &it.Service, &it.TokenPrefix, &it.CreatedAt, &it.LastUsedAt, &it.RevokedAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan service key")
			return
		}
		out = append(out, it)
	}

	c.JSON(http.StatusOK, gin.H{"keys": out})
}

// DeletePayServiceKey revokes a pay-gateway service key. Platform-admin only.
// Revocation is permanent; the next request bearing the revoked key gets 401.
//
// @ID          deletePayServiceKey
// @Summary     Revoke an internal payment-gateway service key (platform-admin only)
// @Description Permanently revokes the key. Platform-admin only.
// @Tags        pay-gateway-admin
// @Produce     json
// @Security    BearerAuth
// @Param       keyId path string true "Key UUID"
// @Success     204   "no content"
// @Failure     403   {object} map[string]string
// @Failure     404   {object} map[string]string
// @Router      /admin/pay/keys/{keyId} [delete]
func (h *Handler) DeletePayServiceKey(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isGod(claims) {
		respondForbidden(c)
		return
	}
	keyID, err := uuid.Parse(c.Param("keyId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	var service string
	err = h.pool.QueryRow(c.Request.Context(),
		`UPDATE pay_service_keys SET revoked_at = now()
		  WHERE id = $1 AND revoked_at IS NULL
		  RETURNING service`,
		keyID,
	).Scan(&service)
	if errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to revoke service key")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		Action:       "RevokePayServiceKey",
		ResourceKind: "PayServiceKey",
		ResourceName: service,
		Outcome:      auditOutcomeSuccess,
		Metadata:     map[string]any{"key_id": keyID, "service": service},
	})

	c.Status(http.StatusNoContent)
}

// registerPayGatewayRoutes wires the phase-1 internal payment gateway. The
// charge routes are public at the router level and key-authenticated inside
// each handler (resolvePayCaller) -- the same construction as the deploy-hook
// consumption routes, because the caller (a bare-VPS bot) has no console
// session. The admin routes are registered on the existing JWT-protected api
// group; the platform-admin check happens inside each handler (isGod), the
// same pattern AssignPlan already uses rather than a separate router-level
// admin group.
func registerPayGatewayRoutes(r *gin.Engine, api gin.IRoutes, h *Handler) {
	r.POST("/api/v1/pay/charges", h.CreateServiceCharge)
	r.GET("/api/v1/pay/charges", h.ListServiceCharges)
	r.GET("/api/v1/pay/charges/:chargeId", h.GetServiceCharge)

	api.POST("/admin/pay/keys", h.CreatePayServiceKey)
	api.GET("/admin/pay/keys", h.ListPayServiceKeys)
	api.DELETE("/admin/pay/keys/:keyId", h.DeletePayServiceKey)
}
