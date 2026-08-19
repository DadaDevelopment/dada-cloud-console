package fincore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/dada-tuda/console/backend/internal/beget"
)

// syncInterval is how often the ongoing push runs. The data it carries moves at
// human speed -- a signup, a payment, a monthly hosting bill -- so an hourly
// pass keeps the CRM current without hammering a customer's production API.
const syncInterval = time.Hour

// internalAccountPredicate names the accounts that are us, not customers: the
// seeded local admins (@dada.local), Keycloak's own service accounts
// (@keycloak.local, service-account-*) and our staff (@dada-tuda.ru). Filing
// them as clients would enter the company into its own CRM as a counterparty.
const internalAccountPredicate = `(
       u.email LIKE '%@dada.local'
    OR u.email LIKE '%@keycloak.local'
    OR u.email LIKE '%@dada-tuda.ru'
    OR u.username LIKE 'service-account-%'
)`

// clientsQuery selects the console users worth existing as CRM clients.
//
// Every signup is deliberately NOT a client. Registration has been open through
// Yandex ID since 2026-08-13 and a bot wave already landed in this table; a
// straight dump would file those farm accounts as counterparties. Owning a
// project or having ever paid is the cheapest evidence of a real person.
//
// Ownership is read from projects.owner_id, not from project_members: in
// production that membership table holds 4 rows against 65 projects and 24
// distinct owners, so filtering on membership alone would file 3 people as
// clients and silently drop the rest.
const clientsQuery = `
SELECT u.id::text,
       u.username,
       COALESCE(u.email, ''),
       COALESCE(u.display_name, ''),
       u.created_at,
       COALESCE(u.signup_channel, ''),
       COALESCE(u.signup_source, '')
FROM users u
WHERE NOT ` + internalAccountPredicate + `
  AND (
       EXISTS (SELECT 1 FROM projects pr WHERE pr.owner_id = u.id)
    OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.user_id = u.id)
    OR EXISTS (SELECT 1 FROM payments p WHERE p.org_id = u.username)
  )
ORDER BY u.created_at`

// internalOrgIDs are the platform's own orgs. Money booked against them is the
// company paying itself -- a test checkout, an internal top-up -- and counting
// it as revenue would print income that no customer ever sent.
var internalOrgIDs = []string{"dada", "internal", "platform"}

// paymentsQuery selects money that actually arrived.
//
// The join goes through payments.org_id -> users.username, not through
// created_by_sub: that column is NOT NULL but empty on every row in production,
// so joining on it links nothing. A personal org's id is the username
// (see internal/auth/jwt.go), which is what makes this join work at all.
//
// includeInternal is the operator's override for the internal-org filter. It
// exists because the filter is a judgement about whose money this is, and that
// judgement should be visible and reversible rather than compiled in.
func paymentsQuery(includeInternal bool) string {
	filter := ""
	if !includeInternal {
		filter = `
  AND NOT (p.org_id = ANY($1::text[]))
  AND COALESCE(u.email, '') NOT LIKE '%@dada-tuda.ru'`
	}
	return `
SELECT p.id::text,
       p.org_id,
       COALESCE(p.plan, ''),
       p.amount_value::text,
       COALESCE(p.currency, 'RUB'),
       COALESCE(p.yk_payment_id, ''),
       COALESCE(p.customer_email, ''),
       COALESCE(p.paid_at, p.updated_at),
       COALESCE(u.id::text, ''),
       COALESCE(u.username, ''),
       COALESCE(u.email, ''),
       COALESCE(u.display_name, ''),
       COALESCE(u.created_at, p.created_at),
       COALESCE(u.signup_channel, ''),
       COALESCE(u.signup_source, '')
FROM payments p
LEFT JOIN users u ON u.username = p.org_id
WHERE p.status = 'succeeded'` + filter + `
ORDER BY p.created_at`
}

// Syncer pushes Dada Cloud's own economics into FinCore.
type Syncer struct {
	pool             *pgxpool.Pool
	client           *Client
	beget            *beget.Client
	begetClusterSlug string

	// hardwareMonthlyRUB is the manually configured hosting bill, used only
	// when the Beget API is not reachable or reports nothing. Zero means the
	// expense is skipped rather than pushed as an invented number.
	hardwareMonthlyRUB float64

	// includeInternal lifts the internal-org filter on payments.
	includeInternal bool

	// projectID pins every fact to one FinCore project so this product's
	// economics stay separable inside Dada Development. Zero leaves the
	// project to FinCore's own classifier.
	projectID int
}

// NewSyncer wires the pusher. It returns nil when FinCore is not configured,
// so the caller can start it unconditionally.
func NewSyncer(pool *pgxpool.Pool, client *Client, begetClient *beget.Client, begetClusterSlug string, hardwareMonthlyRUB float64, projectID int, includeInternal bool) *Syncer {
	if pool == nil || client == nil {
		return nil
	}
	return &Syncer{
		pool:               pool,
		client:             client,
		beget:              begetClient,
		begetClusterSlug:   begetClusterSlug,
		hardwareMonthlyRUB: hardwareMonthlyRUB,
		projectID:          projectID,
		includeInternal:    includeInternal,
	}
}

// Report is what one pass did. Counts come from FinCore's own answer, not from
// how many rows were sent: a transport 200 is not proof the money landed.
type Report struct {
	DryRun bool

	Clients        []ClientUpsert
	ClientsCreated int
	ClientsUpdated int

	Transactions        []Transaction
	TransactionsCreated int
	TransactionsUpdated int
	Unchanged           int

	// PaymentsUnlinked counts succeeded payments whose org resolved to no
	// console user. They are still pushed; they land without a client.
	PaymentsUnlinked int

	// HostingCostRUB is this month's hosting bill as the Beget API prices it.
	//
	// It is measured and reported, never ingested. The company's bank account
	// is already streamed into FinCore by the findata T-Bank integration, and
	// the real outflow to Beget lands there as its own statement (5000 RUB on
	// 2026-08-19, INN 7801451618). Pushing a modelled monthly figure next to it
	// booked the same expense twice with a different number -- see
	// docs/runbooks/fincore-dogfood.md.
	HostingCostRUB float64

	// BegetSkipped explains an absent hosting figure instead of letting a
	// zero read as "we pay nothing".
	BegetSkipped string
}

// Start runs the ongoing push in the background.
func (s *Syncer) Start(ctx context.Context) {
	if s == nil {
		return
	}
	go s.loop(ctx)
}

func (s *Syncer) loop(ctx context.Context) {
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report, err := s.Run(ctx, false)
			if err != nil {
				log.Warn().Err(err).Msg("fincore sync failed")
				continue
			}
			log.Info().
				Int("clients", len(report.Clients)).
				Int("clients_created", report.ClientsCreated).
				Int("transactions", len(report.Transactions)).
				Int("transactions_created", report.TransactionsCreated).
				Float64("hosting_cost_rub", report.HostingCostRUB).
				Int("unchanged", report.Unchanged).
				Msg("fincore sync done")
		}
	}
}

// Run collects the current picture and pushes it. With dryRun the payloads are
// built and returned but nothing is written, which is how a backfill is
// inspected before it touches a live CRM.
func (s *Syncer) Run(ctx context.Context, dryRun bool) (Report, error) {
	report := Report{DryRun: dryRun}

	users, err := s.collectUsers(ctx)
	if err != nil {
		return report, err
	}
	for _, u := range users {
		report.Clients = append(report.Clients, ClientFromUser(u))
	}

	txs, unlinked, err := s.collectPayments(ctx)
	if err != nil {
		return report, err
	}
	report.PaymentsUnlinked = unlinked

	report.HostingCostRUB, report.BegetSkipped = s.collectHostingCost(ctx)

	if s.projectID > 0 {
		for i := range txs {
			txs[i].ProjectID = s.projectID
		}
	}
	report.Transactions = txs

	if dryRun {
		return report, nil
	}

	if len(report.Clients) > 0 {
		res, err := s.client.UpsertClients(ctx, report.Clients)
		if err != nil {
			return report, fmt.Errorf("fincore: client sync: %w", err)
		}
		report.ClientsCreated = res.Created
		report.ClientsUpdated = res.Updated
	}

	if len(report.Transactions) > 0 {
		res, err := s.client.IngestTransactions(ctx, report.Transactions)
		if err != nil {
			return report, fmt.Errorf("fincore: transaction ingest: %w", err)
		}
		report.TransactionsCreated = res.Created
		report.TransactionsUpdated = res.Updated
		report.Unchanged = res.Unchanged
	}

	return report, nil
}

func (s *Syncer) collectUsers(ctx context.Context) ([]CloudUser, error) {
	rows, err := s.pool.Query(ctx, clientsQuery)
	if err != nil {
		return nil, fmt.Errorf("fincore: query users: %w", err)
	}
	defer rows.Close()

	var out []CloudUser
	for rows.Next() {
		var u CloudUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.CreatedAt, &u.SignupChannel, &u.SignupSource); err != nil {
			return nil, fmt.Errorf("fincore: scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Syncer) collectPayments(ctx context.Context) ([]Transaction, int, error) {
	var rows pgx.Rows
	var err error
	if s.includeInternal {
		rows, err = s.pool.Query(ctx, paymentsQuery(true))
	} else {
		rows, err = s.pool.Query(ctx, paymentsQuery(false), internalOrgIDs)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("fincore: query payments: %w", err)
	}
	defer rows.Close()

	var out []Transaction
	unlinked := 0
	for rows.Next() {
		var p CloudPayment
		var owner CloudUser
		if err := rows.Scan(
			&p.ID, &p.OrgID, &p.Plan, &p.Amount, &p.Currency, &p.YKPaymentID, &p.CustomerEmail, &p.PaidAt,
			&owner.ID, &owner.Username, &owner.Email, &owner.DisplayName, &owner.CreatedAt, &owner.SignupChannel, &owner.SignupSource,
		); err != nil {
			return nil, 0, fmt.Errorf("fincore: scan payment: %w", err)
		}
		if owner.ID != "" {
			p.Owner = &owner
		} else {
			unlinked++
		}
		out = append(out, TransactionFromPayment(p))
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, unlinked, nil
}

// collectHostingCost measures this month's hosting bill, or explains why there
// is none. It never returns a guessed figure: the caller reports the reason
// instead, so an absent cost cannot be read as a cheap month.
//
// The number is for the report and the log only. It is not a bank fact and is
// not ingested; the account's real payments reach FinCore through the bank
// integration.
func (s *Syncer) collectHostingCost(ctx context.Context) (float64, string) {
	if s.beget != nil {
		clusters, err := s.beget.ListClusters(ctx)
		if err != nil {
			log.Warn().Err(err).Msg("fincore: beget billing unavailable, falling back to configured hardware cost")
		} else {
			selected := beget.SelectClusters(clusters, s.begetClusterSlug)
			total := 0.0
			for _, cl := range selected {
				total += cl.TotalMonthlyRUB()
			}
			if total > 0 {
				return total, ""
			}
		}
	}

	if s.hardwareMonthlyRUB > 0 {
		return s.hardwareMonthlyRUB, ""
	}

	return 0, "no beget billing source: BEGET_K8S_TOKEN unset or unreadable and HARDWARE_MONTHLY_COST_RUB is 0"
}
