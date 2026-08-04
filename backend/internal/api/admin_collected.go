package api

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// adminMoneyTotals is the platform-wide answer to "what did clients consume vs
// what did we actually collect from them" over the reporting window.
//
// It exists because every other money number on the admin costs page is
// MODELLED: `revenue` is what our consumption formula WOULD charge for observed
// usage, and it is computed for a free-plan account exactly the same way as for
// a paying one. Read alone it says the platform earns money it has never seen.
// Paid is the only figure here backed by a settled payment; Metered is the only
// one backed by a per-hour ledger row rather than a run-rate projection.
type adminMoneyTotals struct {
	PaidRUB        float64 `json:"paid_rub"`
	MeteredRUB     float64 `json:"metered_rub"`
	UncollectedRUB float64 `json:"uncollected_rub"`
	MeteredSince   string  `json:"metered_since,omitempty"`
	LedgerHours    int     `json:"ledger_hours"`
}

// adminPlanFree is the plan key assumed for an org with no billing_accounts
// row, mirroring planFor (billing.go): absent row means never assigned, and an
// unassigned org is on free.
const adminPlanFree = "free"

// attachClientMoney fills the collected-vs-consumed columns on every customer
// node of the cost tree and returns the platform-wide totals.
//
// Per client: Plan/PlanPriceRUB come from the billing_accounts row of each org
// that owns one of their projects (window-scaled from the monthly subscription
// price), PaidRUB from payments actually settled inside the window, MeteredRUB
// from the app_usage ledger summed over their projects. UncollectedRUB is
// Metered minus Paid and is deliberately signed: a client who overpaid shows
// negative, and hiding that behind a floor would make the column a one-way
// accusation instead of a balance.
//
// Best-effort by design: any failed lookup leaves the columns at zero and the
// rest of the economics page intact. These numbers are a diagnosis of the
// business, not an input to a charge, so a Postgres hiccup must not blank the
// cost tree that is the page's primary content.
func (h *Handler) attachClientMoney(ctx context.Context, clients []*adminCostClient, days int) adminMoneyTotals {
	var totals adminMoneyTotals
	from := h.nowUTC().AddDate(0, 0, -days)

	orgOf, err := h.adminProjectOrgs(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("admin costs: collected-money columns skipped, project->org lookup failed")
		return totals
	}
	plans, err := h.adminOrgPlans(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("admin costs: collected-money columns skipped, plan lookup failed")
		return totals
	}
	paid, err := h.adminOrgPaid(ctx, from)
	if err != nil {
		log.Warn().Err(err).Msg("admin costs: collected-money columns skipped, payments lookup failed")
		return totals
	}
	metered, since, hours, err := h.adminProjectMetered(ctx, from)
	if err != nil {
		log.Warn().Err(err).Msg("admin costs: metered column skipped, app_usage read failed")
	}

	planPrice := map[string]float64{}
	for _, p := range h.billingPlans {
		planPrice[p.Key] = p.PriceRUB
	}
	windowScale := float64(days) / billingMonthDays

	for _, cl := range clients {
		orgs := map[string]struct{}{}
		var clientMetered float64
		for i := range cl.Projects {
			p := &cl.Projects[i]
			p.MeteredRUB = round2(metered[p.ProjectID])
			clientMetered += p.MeteredRUB
			if org := orgOf[p.ProjectID]; org != "" {
				orgs[org] = struct{}{}
			}
		}

		planKeys := make([]string, 0, len(orgs))
		var subscription, collected float64
		for org := range orgs {
			key := plans[org]
			if key == "" {
				key = adminPlanFree
			}
			planKeys = append(planKeys, key)
			subscription += planPrice[key] * windowScale
			collected += paid[org]
		}
		sort.Strings(planKeys)

		cl.Plan = strings.Join(dedupeSorted(planKeys), "+")
		cl.PlanPriceRUB = round2(subscription)
		cl.PaidRUB = round2(collected)
		cl.MeteredRUB = round2(clientMetered)
		cl.UncollectedRUB = round2(clientMetered - collected)

		totals.PaidRUB += cl.PaidRUB
		totals.MeteredRUB += cl.MeteredRUB
	}

	totals.PaidRUB = round2(totals.PaidRUB)
	totals.MeteredRUB = round2(totals.MeteredRUB)
	totals.UncollectedRUB = round2(totals.MeteredRUB - totals.PaidRUB)
	totals.LedgerHours = hours
	if !since.IsZero() {
		totals.MeteredSince = since.Format(time.RFC3339)
	}
	return totals
}

// dedupeSorted collapses runs of equal strings in an already-sorted slice. Used
// for the plan label: a client whose projects all sit in one org must read
// "free", not "free+free+free".
func dedupeSorted(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i > 0 && s == in[i-1] {
			continue
		}
		out = append(out, s)
	}
	return out
}

// adminProjectOrgs maps every project to the org that owns it. Projects with no
// org (pre-migration-021 leftovers) are skipped: they cannot be joined to a
// billing account, and inventing one would attribute someone else's payment.
func (h *Handler) adminProjectOrgs(ctx context.Context) (map[string]string, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT id::text, COALESCE(org_id, '') FROM projects`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, org string
		if err := rows.Scan(&id, &org); err != nil {
			return nil, err
		}
		if org == "" {
			continue
		}
		out[id] = org
	}
	return out, rows.Err()
}

// adminOrgPlans returns each org's assigned plan key. An org missing from the
// map is on free, same rule as planFor.
func (h *Handler) adminOrgPlans(ctx context.Context) (map[string]string, error) {
	rows, err := h.pool.Query(ctx, `SELECT org_id, plan FROM billing_accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var org, plan string
		if err := rows.Scan(&org, &plan); err != nil {
			return nil, err
		}
		out[org] = plan
	}
	return out, rows.Err()
}

// adminOrgPaid sums money that actually settled per org since `from`. Only
// status='succeeded' counts: a pending YooKassa payment is an intention, and a
// dashboard that counts intentions as revenue is how a platform convinces
// itself it is being paid. paid_at is the settlement instant; rows without one
// fall back to created_at so a succeeded row can never vanish from the window.
func (h *Handler) adminOrgPaid(ctx context.Context, from time.Time) (map[string]float64, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT org_id, COALESCE(SUM(amount_value), 0)::float8
		FROM payments
		WHERE status = 'succeeded' AND COALESCE(paid_at, created_at) >= $1
		GROUP BY org_id`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var org string
		var sum float64
		if err := rows.Scan(&org, &sum); err != nil {
			return nil, err
		}
		out[org] = sum
	}
	return out, rows.Err()
}

// adminProjectMetered sums the app_usage ledger per project since `from`, and
// reports the oldest hour the ledger holds plus how many distinct hours it
// covers. The two extra returns are not decoration: the ledger starts empty and
// fills one hour at a time, so a zero Metered column is ambiguous between "this
// client consumed nothing" and "the meter has not run yet". The UI needs to be
// able to say which.
//
// Rows with a NULL project (the ledger deliberately outlives the project it
// describes) are dropped: they cannot be attributed to a client, and hanging an
// orphan off a live one would be a worse error than omitting it.
func (h *Handler) adminProjectMetered(ctx context.Context, from time.Time) (map[string]float64, time.Time, int, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT project_id::text, COALESCE(SUM(cost_rub), 0)::float8
		FROM app_usage
		WHERE hour_start >= $1 AND project_id IS NOT NULL
		GROUP BY project_id`, from)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var pid string
		var sum float64
		if err := rows.Scan(&pid, &sum); err != nil {
			return nil, time.Time{}, 0, err
		}
		out[pid] = sum
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, 0, err
	}

	var since *time.Time
	var hours int
	if err := h.pool.QueryRow(ctx, `
		SELECT MIN(hour_start), COUNT(DISTINCT hour_start)
		FROM app_usage WHERE hour_start >= $1`, from,
	).Scan(&since, &hours); err != nil {
		return out, time.Time{}, 0, err
	}
	if since == nil {
		return out, time.Time{}, hours, nil
	}
	return out, since.UTC(), hours, nil
}
