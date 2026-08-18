package api

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/billing/tbank"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

//go:embed templates/invoice.html.tmpl
var invoiceTemplateFS embed.FS

var invoiceTemplate = template.Must(template.ParseFS(invoiceTemplateFS, "templates/invoice.html.tmpl"))

// invoiceViewData feeds templates/invoice.html.tmpl.
type invoiceViewData struct {
	InvoiceNumber       string
	IssuedAt            string
	AmountValue         string
	Plan                string
	PlatformName        string
	PlatformINN         string
	PlatformKPP         string
	PlatformOGRN        string
	PlatformAddress     string
	PlatformBankAccount string
	PlatformBankBIC     string
	PlatformBankName    string
	PlatformCorrAccount string
	PayerOrgName        string
	PayerINN            string
	PayerKPP            string
	PayerAddress        string
}

// CreateInvoice starts a bank-transfer (invoice) payment for a paid plan on
// the org owning the project. Requires write role, same as BillingCheckout.
// Unlike BillingCheckout, no redirect happens here: the caller is expected
// to open GetInvoice's printable page and pay by bank transfer, and the
// tbank statement-reconcile loop is what eventually marks it succeeded.
//
// @ID          createInvoice
// @Summary     Start a plan invoice payment
// @Description Validates the payer's INN and creates a pending invoice payment for the requested plan. Returns the invoice number and the printable invoice URL. Requires write role on the project.
// @Tags        billing
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string            true "Project UUID"
// @Param       body      body     map[string]string true "Plan key and payer requisites"
// @Success     200       {object} map[string]interface{}
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     500       {object} map[string]string
// @Router      /projects/{projectId}/billing/invoice [post]
func (h *Handler) CreateInvoice(c *gin.Context) {
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

	var body struct {
		Plan              string `json:"plan" binding:"required"`
		PayerINN          string `json:"payer_inn" binding:"required"`
		PayerKPP          string `json:"payer_kpp"`
		PayerOrgName      string `json:"payer_org_name" binding:"required"`
		PayerLegalAddress string `json:"payer_legal_address" binding:"required"`
		Email             string `json:"email"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if body.Plan == "free" || body.Plan == "enterprise" {
		respondError(c, http.StatusBadRequest, "plan is not payable: "+body.Plan)
		return
	}
	if err := tbank.ValidateINN(body.PayerINN); err != nil {
		respondErrorCode(c, http.StatusBadRequest, "invalid_inn", "ИНН не прошёл проверку контрольной суммы")
		return
	}
	var plan *pricing.Plan
	for i := range h.billingPlans {
		if h.billingPlans[i].Key == body.Plan {
			plan = &h.billingPlans[i]
			break
		}
	}
	if plan == nil {
		respondError(c, http.StatusBadRequest, "unknown plan key: "+body.Plan)
		return
	}

	if h.tbank == nil {
		respondError(c, http.StatusConflict, "payments_not_configured")
		return
	}

	orgID, err := h.projectOrg(c.Request.Context(), projectID)
	if err != nil {
		respondNotFound(c)
		return
	}
	if orgID == "" {
		respondError(c, http.StatusConflict, "org_unresolved")
		return
	}

	now := time.Now().UTC()
	invoiceNumber, err := tbank.GenerateInvoiceNumber(c.Request.Context(), h.pool, now)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to generate invoice number")
		return
	}

	paymentID := uuid.New().String()
	amount := plan.PriceRUB
	_, err = h.pool.Exec(c.Request.Context(), `
		INSERT INTO payments (id, org_id, plan, amount_value, currency, status, payment_method,
		                       invoice_number, payer_inn, payer_kpp, payer_org_name, payer_legal_address,
		                       customer_email, created_by_sub, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'RUB', 'pending', 'invoice', $5, $6, $7, $8, $9, $10, $11, $12, $12)
	`, paymentID, orgID, plan.Key, amount, invoiceNumber, body.PayerINN, body.PayerKPP,
		body.PayerOrgName, body.PayerLegalAddress, body.Email, claims.Subject, now)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create invoice payment")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"payment_id":     paymentID,
		"invoice_number": invoiceNumber,
		"invoice_url":    "/api/v1/billing/invoice/" + paymentID,
	})
}

// GetInvoice renders a printable HTML invoice page for one payment. The
// caller must belong to the org that owns the payment (or be god). There is
// no PDF generation: the page is meant to be saved via the browser's own
// print-to-PDF, so the layout stays plain, print-friendly HTML.
//
// @ID          getInvoice
// @Summary     Get a printable invoice
// @Description Renders a printable HTML page for one invoice payment. Requires membership in the org that owns the payment.
// @Tags        billing
// @Produce     html
// @Security    BearerAuth
// @Param       paymentId path string true "Payment UUID"
// @Success     200       {string} string "HTML page"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     500       {object} map[string]string
// @Router      /billing/invoice/{paymentId} [get]
func (h *Handler) GetInvoice(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	paymentID, err := uuid.Parse(c.Param("paymentId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	var (
		orgID, plan, amountText, invoiceNumber string
		payerINN, payerKPP, payerOrgName       string
		payerLegalAddress                      string
		createdAt                              time.Time
	)
	err = h.pool.QueryRow(c.Request.Context(), `
		SELECT org_id, plan, amount_value::text, coalesce(invoice_number, ''),
		       coalesce(payer_inn, ''), coalesce(payer_kpp, ''), coalesce(payer_org_name, ''),
		       coalesce(payer_legal_address, ''), created_at
		FROM payments WHERE id = $1 AND payment_method = 'invoice'
	`, paymentID).Scan(&orgID, &plan, &amountText, &invoiceNumber,
		&payerINN, &payerKPP, &payerOrgName, &payerLegalAddress, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load invoice")
		return
	}

	if !isGod(claims) && claims.OrgRole(orgID) == "" {
		respondForbidden(c)
		return
	}

	data := invoiceViewData{
		InvoiceNumber:       invoiceNumber,
		IssuedAt:            createdAt.Format("02.01.2006"),
		AmountValue:         amountText,
		Plan:                plan,
		PlatformName:        h.cfg.PlatformName,
		PlatformINN:         h.cfg.PlatformINN,
		PlatformKPP:         h.cfg.PlatformKPP,
		PlatformOGRN:        h.cfg.PlatformOGRN,
		PlatformAddress:     h.cfg.PlatformLegalAddress,
		PlatformBankAccount: h.cfg.PlatformBankAccount,
		PlatformBankBIC:     h.cfg.PlatformBankBIC,
		PlatformBankName:    h.cfg.PlatformBankName,
		PlatformCorrAccount: h.cfg.PlatformCorrAccount,
		PayerOrgName:        payerOrgName,
		PayerINN:            payerINN,
		PayerKPP:            payerKPP,
		PayerAddress:        payerLegalAddress,
	}

	var buf bytes.Buffer
	if err := invoiceTemplate.Execute(&buf, data); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to render invoice")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", buf.Bytes())
}
