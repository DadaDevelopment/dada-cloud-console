package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/billing/yookassa"
	"github.com/dada-tuda/console/backend/internal/crypto"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// paymentWebhookEntry is one registered YooKassa webhook subscription, stored
// as an element of payment_connections.webhook_ids (JSONB array).
type paymentWebhookEntry struct {
	ID    string `json:"id"`
	Event string `json:"event"`
}

// paymentsRedirectBase builds the settings-page URL the callback always
// redirects the browser to, success or error.
func paymentsRedirectBase(projectID uuid.UUID, appName string) string {
	return "/projects/" + projectID.String() + "/apps/" + appName + "/settings?tab=payments"
}

// PaymentsConnect starts the YooKassa Partners OAuth flow for one app: it
// inserts a one-time state row and returns the authorize URL for the
// frontend to navigate the browser to. Requires write access. Returns 409
// when the platform has no partner OAuth app registered.
//
// @ID          paymentsConnect
// @Summary     Start the YooKassa payments connect flow for an app
// @Description Returns a YooKassa Partners OAuth authorize URL. canWrite required. 409 when partner OAuth credentials are unconfigured.
// @Tags        payments
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} map[string]string "object with an authorize_url field"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/payments/connect [post]
func (h *Handler) PaymentsConnect(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	appName := c.Param("appName")

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !canWrite(role) {
		respondForbidden(c)
		return
	}

	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}

	var appExists bool
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM resource_snapshots WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3)`,
		projectID, envID, appName,
	).Scan(&appExists); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify app")
		return
	}
	if !appExists {
		respondNotFound(c)
		return
	}

	if h.cfg.YooKassaPartnerClientID == "" || h.cfg.YooKassaPartnerClientSecret == "" {
		respondError(c, http.StatusConflict, "payments_not_configured")
		return
	}

	state := randomHex(24)
	if _, err := h.pool.Exec(c.Request.Context(),
		`INSERT INTO payment_oauth_states (state, project_id, environment_id, app_name, user_sub)
		 VALUES ($1, $2, $3, $4, $5)`,
		state, projectID, envID, appName, claims.UserID.String(),
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to start payments connect")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"authorize_url": h.yookassaOAuth.AuthorizeURL(h.cfg.YooKassaPartnerClientID, state),
	})
}

// PaymentsOAuthCallback is the public YooKassa Partners OAuth callback.
// YooKassa redirects the browser here with code + our one-time state. It
// consumes the state (one-time, DELETE RETURNING + TTL check), exchanges the
// code, fetches the merchant identity, encrypts and upserts the connection
// row, injects the runtime env vars, registers webhooks against the app's
// public hostname when one exists, then 302s the browser back to the app's
// payments settings tab. Every failure path also 302s (with a
// payments_error query param) -- this is a browser flow, never JSON.
//
// Public (no bearer): trust is the one-time state row, exactly like the
// GitHub OAuth callback (git_oauth.go).
//
// @ID          paymentsOAuthCallback
// @Summary     YooKassa payments connect callback
// @Description Public endpoint YooKassa redirects to after partner OAuth authorization. Consumes the one-time state, exchanges the code, stores the connection, registers webhooks, redirects to the app settings page.
// @Tags        payments
// @Param       code  query string true "YooKassa OAuth code"
// @Param       state query string true "One-time state"
// @Success     302
// @Router      /payments/oauth/callback [get]
func (h *Handler) PaymentsOAuthCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	if state == "" || code == "" {
		c.Redirect(http.StatusFound, "/?payments_error=missing_code_or_state")
		return
	}

	var projectID, envID uuid.UUID
	var appName, userSub string
	var createdAt time.Time
	err := h.pool.QueryRow(c.Request.Context(),
		`DELETE FROM payment_oauth_states WHERE state = $1
		 RETURNING project_id, environment_id, app_name, user_sub, created_at`,
		state,
	).Scan(&projectID, &envID, &appName, &userSub, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		c.Redirect(http.StatusFound, "/?payments_error=invalid_or_expired_state")
		return
	}
	if err != nil {
		log.Printf("payments: callback state lookup failed: %v", err)
		c.Redirect(http.StatusFound, "/?payments_error=internal")
		return
	}
	if time.Since(createdAt) > oauthStateTTL {
		c.Redirect(http.StatusFound, "/?payments_error=invalid_or_expired_state")
		return
	}

	redirectBase := paymentsRedirectBase(projectID, appName)

	if h.cfg.YooKassaPartnerClientID == "" || h.cfg.YooKassaPartnerClientSecret == "" {
		c.Redirect(http.StatusFound, redirectBase+"&payments_error=payments_not_configured")
		return
	}

	accessToken, expiresIn, err := h.yookassaOAuth.ExchangeCode(
		c.Request.Context(), h.cfg.YooKassaPartnerClientID, h.cfg.YooKassaPartnerClientSecret, code)
	if err != nil {
		log.Printf("payments: exchange code failed project=%s app=%s: %v", projectID, appName, err)
		c.Redirect(http.StatusFound, redirectBase+"&payments_error=exchange_failed")
		return
	}

	accountID, meRaw, err := h.yookassaOAuth.Me(c.Request.Context(), accessToken)
	if err != nil {
		log.Printf("payments: me lookup failed project=%s app=%s: %v", projectID, appName, err)
		c.Redirect(http.StatusFound, redirectBase+"&payments_error=identity_failed")
		return
	}

	encToken, err := crypto.EncryptToken(h.cfg.GitopsEncryptionKey, []byte(accessToken))
	if err != nil {
		log.Printf("payments: encrypt access token failed project=%s app=%s: %v", projectID, appName, err)
		c.Redirect(http.StatusFound, redirectBase+"&payments_error=internal")
		return
	}
	encTokenText := yookassa.EncodeAccessToken(encToken)

	var expiresAt *time.Time
	if expiresIn > 0 {
		t := time.Now().UTC().Add(time.Duration(expiresIn) * time.Second)
		expiresAt = &t
	}

	webhookIDs, webhookNote := h.registerPaymentWebhooks(c.Request.Context(), envID, appName, accessToken)
	webhookIDsJSON, err := json.Marshal(webhookIDs)
	if err != nil {
		webhookIDsJSON = []byte("[]")
	}

	connID := uuid.New()
	if _, err := h.pool.Exec(c.Request.Context(), `
		INSERT INTO payment_connections
		  (id, project_id, environment_id, app_name, account_id, me_raw, access_token_enc,
		   expires_at, status, webhook_ids, webhook_note, connected_by_sub)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'active', $9, $10, $11)
		ON CONFLICT (environment_id, app_name) DO UPDATE
		   SET account_id       = EXCLUDED.account_id,
		       me_raw           = EXCLUDED.me_raw,
		       access_token_enc = EXCLUDED.access_token_enc,
		       expires_at       = EXCLUDED.expires_at,
		       status           = 'active',
		       webhook_ids      = EXCLUDED.webhook_ids,
		       webhook_note     = EXCLUDED.webhook_note,
		       connected_by_sub = EXCLUDED.connected_by_sub,
		       updated_at       = NOW()
	`, connID, projectID, envID, appName, accountID, meRaw, encTokenText,
		expiresAt, webhookIDsJSON, webhookNote, userSub); err != nil {
		log.Printf("payments: store connection failed project=%s app=%s: %v", projectID, appName, err)
		c.Redirect(http.StatusFound, redirectBase+"&payments_error=internal")
		return
	}

	if _, err := h.upsertEnvVar(c.Request.Context(), envID, appName, "YOOKASSA_OAUTH_TOKEN", accessToken, true, "runtime", userSub); err != nil {
		log.Printf("payments: env inject YOOKASSA_OAUTH_TOKEN failed project=%s app=%s: %v", projectID, appName, err)
	}
	if _, err := h.upsertEnvVar(c.Request.Context(), envID, appName, "YOOKASSA_ACCOUNT_ID", accountID, false, "runtime", userSub); err != nil {
		log.Printf("payments: env inject YOOKASSA_ACCOUNT_ID failed project=%s app=%s: %v", projectID, appName, err)
	}

	c.Redirect(http.StatusFound, redirectBase+"&connected=1")
}

// registerPaymentWebhooks resolves the app's public hostname (managed
// surrogate domain preferred, else any attached hostname) and, if one
// exists, registers YooKassa webhooks for payment.succeeded and
// payment.canceled against it. A worker/no-HTTP app with no hostname is not
// an error -- it returns an empty slice and a "no_public_hostname" note.
func (h *Handler) registerPaymentWebhooks(ctx context.Context, envID uuid.UUID, appName, accessToken string) ([]paymentWebhookEntry, *string) {
	var host string
	err := h.pool.QueryRow(ctx,
		`SELECT hostname FROM domain_hostnames
		 WHERE environment_id = $1 AND app_name = $2
		 ORDER BY managed DESC, hostname LIMIT 1`,
		envID, appName,
	).Scan(&host)
	if errors.Is(err, pgx.ErrNoRows) || host == "" {
		note := "no_public_hostname"
		return []paymentWebhookEntry{}, &note
	}
	if err != nil {
		log.Printf("payments: hostname lookup failed env=%s app=%s: %v", envID, appName, err)
		note := "no_public_hostname"
		return []paymentWebhookEntry{}, &note
	}

	webhookURL := "https://" + host + "/yookassa/webhook"
	events := []string{"payment.succeeded", "payment.canceled"}
	entries := make([]paymentWebhookEntry, 0, len(events))
	for _, event := range events {
		id, err := h.yookassaOAuth.RegisterWebhook(ctx, accessToken, event, webhookURL)
		if err != nil {
			log.Printf("payments: register webhook %s failed env=%s app=%s: %v", event, envID, appName, err)
			continue
		}
		entries = append(entries, paymentWebhookEntry{ID: id, Event: event})
	}
	return entries, nil
}

// paymentsStatusResponse is GET .../payments. The access token is never
// returned; env_keys only names the injected keys.
type paymentsStatusResponse struct {
	Status      string                `json:"status"`
	AccountID   string                `json:"account_id,omitempty"`
	ExpiresAt   *time.Time            `json:"expires_at,omitempty"`
	Webhooks    []paymentWebhookEntry `json:"webhooks"`
	WebhookNote string                `json:"webhook_note,omitempty"`
	EnvKeys     []string              `json:"env_keys"`
	ConnectedAt time.Time             `json:"connected_at"`
}

// PaymentsStatus returns the connection status for an app. Read-only, any
// project member. The access token is never included in the response.
//
// @ID          paymentsStatus
// @Summary     Get the payments connection status for an app
// @Description Returns the YooKassa connection status, merchant account id, webhook registrations and injected env var keys. The access token is never returned.
// @Tags        payments
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/payments [get]
func (h *Handler) PaymentsStatus(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	appName := c.Param("appName")

	if _, err := h.effectiveRole(c.Request.Context(), claims, projectID); errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}

	var accountID *string
	var expiresAt *time.Time
	var status, webhookNote *string
	var webhookIDsRaw []byte
	var connectedAt time.Time
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT account_id, expires_at, status, webhook_ids, webhook_note, created_at
		 FROM payment_connections WHERE environment_id = $1 AND app_name = $2`,
		envID, appName,
	).Scan(&accountID, &expiresAt, &status, &webhookIDsRaw, &webhookNote, &connectedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusOK, paymentsStatusResponse{Status: "disconnected", Webhooks: []paymentWebhookEntry{}})
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load payments connection")
		return
	}

	var webhooks []paymentWebhookEntry
	_ = json.Unmarshal(webhookIDsRaw, &webhooks)
	if webhooks == nil {
		webhooks = []paymentWebhookEntry{}
	}

	resp := paymentsStatusResponse{
		Webhooks:    webhooks,
		EnvKeys:     []string{"YOOKASSA_OAUTH_TOKEN", "YOOKASSA_ACCOUNT_ID"},
		ConnectedAt: connectedAt,
	}
	if status != nil {
		resp.Status = *status
	}
	if accountID != nil {
		resp.AccountID = *accountID
	}
	if webhookNote != nil {
		resp.WebhookNote = *webhookNote
	}
	resp.ExpiresAt = expiresAt

	c.JSON(http.StatusOK, resp)
}

// PaymentsDisconnect removes a payments connection: best-effort webhook
// deletion (decrypted token), env var cleanup, then the connection row
// itself. YooKassa API errors during webhook deletion are logged, never
// fatal -- the local row must always be removable even if YooKassa is down.
//
// @ID          paymentsDisconnect
// @Summary     Disconnect payments for an app
// @Description Best-effort deletes the registered webhooks, removes the injected env vars, and deletes the connection row. Requires write access.
// @Tags        payments
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     204       {object} nil
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/payments [delete]
func (h *Handler) PaymentsDisconnect(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	appName := c.Param("appName")

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !canWrite(role) {
		respondForbidden(c)
		return
	}

	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}

	var connID uuid.UUID
	var encTokenText string
	var webhookIDsRaw []byte
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT id, access_token_enc, webhook_ids FROM payment_connections
		 WHERE environment_id = $1 AND app_name = $2`,
		envID, appName,
	).Scan(&connID, &encTokenText, &webhookIDsRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load payments connection")
		return
	}

	var webhooks []paymentWebhookEntry
	_ = json.Unmarshal(webhookIDsRaw, &webhooks)
	if len(webhooks) > 0 {
		if encToken, decErr := yookassa.DecodeAccessToken(encTokenText); decErr == nil {
			if plain, decErr := crypto.DecryptToken(h.cfg.GitopsEncryptionKey, encToken); decErr == nil {
				accessToken := string(plain)
				for _, wh := range webhooks {
					if delErr := h.yookassaOAuth.DeleteWebhook(c.Request.Context(), accessToken, wh.ID); delErr != nil {
						log.Printf("payments: delete webhook %s failed env=%s app=%s: %v", wh.ID, envID, appName, delErr)
					}
				}
			} else {
				log.Printf("payments: decrypt token for webhook cleanup failed env=%s app=%s: %v", envID, appName, decErr)
			}
		} else {
			log.Printf("payments: decode token for webhook cleanup failed env=%s app=%s: %v", envID, appName, decErr)
		}
	}

	for _, key := range []string{"YOOKASSA_OAUTH_TOKEN", "YOOKASSA_ACCOUNT_ID"} {
		if _, err := h.pool.Exec(c.Request.Context(),
			`DELETE FROM env_vars WHERE environment_id = $1 AND app_name = $2 AND key = $3`,
			envID, appName, key,
		); err != nil {
			log.Printf("payments: delete env var %s failed env=%s app=%s: %v", key, envID, appName, err)
		}
	}

	if _, err := h.pool.Exec(c.Request.Context(),
		`DELETE FROM payment_connections WHERE id = $1`, connID,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to delete payments connection")
		return
	}

	c.Status(http.StatusNoContent)
}
