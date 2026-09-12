package api

import (
	"context"
	"log"
	"strconv"
)

// daysArg renders the window for the `($1 || ' days')::interval` form the
// journey queries use. Passing an int would make Postgres infer integer for $1
// and fail the concatenation.
func daysArg(days int) string { return strconv.Itoa(days) }

// journeyStage is one step of the single-spine funnel: a stable key, the label
// shown on the diagram, how many people reached it, and where the number comes
// from.
//
// Value counts PEOPLE at every stage without exception. That is the whole
// point of this funnel: the previous one changed unit three times (Metrika
// visits, then Metrika users, then accounts, then billing organizations) and
// every change of unit was a seam a reader had to be warned about in prose.
type journeyStage struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Value  int    `json:"value"`
	Source string `json:"source"`
}

// journeySlice is a breakdown WITHIN one stage, never a stage of its own:
// which signup door people used, which resource kinds they asked for. Drawn
// under the spine rather than as a parallel lane, because the parts overlap
// and do not sum to the stage above them.
type journeySlice struct {
	StageKey string             `json:"stage_key"`
	Label    string             `json:"label"`
	Parts    []journeySlicePart `json:"parts"`
}

type journeySlicePart struct {
	Key   string `json:"key"`
	Value int    `json:"value"`
}

// adminFunnelJourney is the seamless funnel: one identity, one unit, one
// source. Metrika does not appear here at all -- it is reported separately as
// an external cross-check, because its numbers cannot be joined to a person.
//
// DarkSegment names the one place the chain is still blind, so the gap is
// stated as data instead of being hidden by a smooth ribbon.
type adminFunnelJourney struct {
	Days        int            `json:"days"`
	Stages      []journeyStage `json:"stages"`
	Slices      []journeySlice `json:"slices"`
	DarkSegment string         `json:"dark_segment,omitempty"`
}

// journeyIdentityCTE resolves every row to ONE identity column.
//
// A person is their account id once an account exists, and their browser id
// until then. ux_identity (migration 077) carries the crossing: anon_id lives
// in localStorage and survives the Keycloak round trip, so the same browser is
// present on both sides of a login, and a browser that signed into two
// accounts resolves to neither -- a shared machine stays anonymous rather than
// putting one person's pre-login walk on another person's funnel.
//
// This is why the funnel has no seam between "anonymous visitor" and
// "account": the join is real and it is in the database.
const journeyIdentityCTE = `
	ident AS (
		SELECT
			COALESCE(x.user_id::text, i.user_id::text, x.anon_id::text) AS pid,
			x.event_type, x.target, x.path
		FROM ux_events x
		LEFT JOIN ux_identity i ON i.anon_id = x.anon_id
		WHERE x.occurred_at >= now() - ($1 || ' days')::interval
	),
	people AS (SELECT DISTINCT pid FROM ident WHERE pid IS NOT NULL)
`

// adminFunnelJourneyQuery counts the same set of people through every stage.
//
// Ownership, not organization, is what carries the billing stages: payments
// are stored per org_id, but an org reaches checkout because a person clicked,
// and projects.owner_id is that person. Reporting the org count instead forced
// a unit change at the last stage for no gain -- live data has the same 2 and
// 1 either way.
func adminFunnelJourneyQuery() string {
	return `
	WITH` + journeyIdentityCTE + `,
	acct AS (
		SELECT u.id::text AS pid FROM user_accounts u
		WHERE u.account_kind = 'customer'
		  AND ($2::text[] IS NULL OR u.account_kind <> ALL($2))
	),
	proj AS (
		SELECT p.owner_id::text AS pid, p.id, p.org_id
		FROM projects p JOIN acct a ON a.pid = p.owner_id::text
	),
	req AS (
		SELECT DISTINCT p.pid FROM operations o JOIN proj p ON p.id = o.project_id
		WHERE o.action IN ('CreateApp', 'CreateServiceDatabase', 'CreateS3Bucket', 'CreateAppServer', 'BoxUp')
		  AND o.status NOT IN ('Failed', 'Cancelled')
	),
	rdy AS (
		SELECT DISTINCT p.pid FROM proj p JOIN resource_snapshots rs ON rs.project_id = p.id
			WHERE rs.phase = 'Ready' AND rs.kind IN ('App', 'ServiceDatabase', 'ServiceDatabaseV2', 'S3Bucket')
		UNION
		SELECT DISTINCT p.pid FROM proj p JOIN app_servers s ON s.project_id = p.id WHERE s.status = 'Ready'
		UNION
		SELECT DISTINCT p.pid FROM proj p JOIN boxes b ON b.project_id = p.id WHERE b.status IN ('Ready', 'Idle')
	),
	chk AS (
		SELECT DISTINCT p.pid FROM proj p JOIN payments y ON y.org_id = p.org_id
		WHERE y.status IN ('pending', 'succeeded', 'canceled')
	),
	paid AS (
		SELECT DISTINCT p.pid FROM proj p JOIN payments y ON y.org_id = p.org_id
		WHERE y.status = 'succeeded' AND y.paid_at IS NOT NULL
	)
	SELECT
		(SELECT count(*) FROM people),
		(SELECT count(DISTINCT pid) FROM ident WHERE event_type = 'pageview'),
		(SELECT count(DISTINCT pid) FROM ident WHERE path = '/login'),
		(SELECT count(DISTINCT pid) FROM ident WHERE path = '/callback'),
		(SELECT count(*) FROM people WHERE pid IN (SELECT pid FROM acct)),
		(SELECT count(*) FROM people WHERE pid IN (SELECT pid FROM proj)),
		(SELECT count(*) FROM people WHERE pid IN (SELECT pid FROM req)),
		(SELECT count(*) FROM people WHERE pid IN (SELECT pid FROM rdy)),
		(SELECT count(*) FROM people WHERE pid IN (SELECT pid FROM chk)),
		(SELECT count(*) FROM people WHERE pid IN (SELECT pid FROM paid))
	`
}

// adminFunnelJourneySlicesQuery breaks two stages down without adding stages.
//
// Both breakdowns are restricted to the people currently in the funnel window,
// so a slice can never exceed the stage it sits under -- the failure mode that
// made the old per-resource lanes read as growth.
func adminFunnelJourneySlicesQuery() string {
	return `
	WITH` + journeyIdentityCTE + `,
	doors AS (
		SELECT COALESCE(u.signup_channel, 'не записан') AS key, count(*)::int AS value
		FROM user_accounts u
		WHERE u.account_kind = 'customer'
		  AND ($2::text[] IS NULL OR u.account_kind <> ALL($2))
		  AND u.id::text IN (SELECT pid FROM people)
		GROUP BY 1
	),
	kinds AS (
		SELECT 'app'::text AS key, 'CreateApp'::text AS action
		UNION ALL SELECT 'db', 'CreateServiceDatabase'
		UNION ALL SELECT 'vm', 'CreateAppServer'
		UNION ALL SELECT 'box', 'BoxUp'
		UNION ALL SELECT 's3', 'CreateS3Bucket'
	),
	res AS (
		SELECT k.key, count(DISTINCT p.owner_id)::int AS value
		FROM kinds k
		JOIN operations o ON o.action = k.action AND o.status NOT IN ('Failed', 'Cancelled')
		JOIN projects p ON p.id = o.project_id
		WHERE p.owner_id::text IN (SELECT pid FROM people)
		GROUP BY k.key
	)
	SELECT 'account'::text AS stage_key, key, value FROM doors
	UNION ALL
	SELECT 'requested', key, value FROM res
	ORDER BY 1, 3 DESC
	`
}

// journeyStageSpec names the stages in spine order. Kept next to the query so
// a stage can never be renamed in one place and not the other.
var journeyStageSpec = []struct {
	key, label, source string
}{
	{"touched", "Зашли на сайт", "ux_events"},
	{"viewed", "Открыли страницу", "ux_events"},
	{"login", "Открыли вход", "ux_events"},
	{"back", "Вернулись после входа", "ux_events"},
	{"account", "Есть аккаунт", "ux_events + БД"},
	{"project", "Создали проект", "ux_events + БД"},
	{"requested", "Запросили ресурс", "ux_events + БД"},
	{"ready", "Ресурс работает", "ux_events + БД"},
	{"checkout", "Дошли до оплаты", "ux_events + БД"},
	{"paid", "Оплатили", "ux_events + БД"},
}

// journeySliceLabels names each breakdown row group for the UI.
var journeySliceLabels = map[string]string{
	"account":   "Через какую дверь зарегистрировались",
	"requested": "Какие ресурсы запросили",
}

// adminFunnelJourneyReport assembles the seamless funnel for the window.
//
// Best-effort like the other funnel legs: a failure leaves the stages empty
// rather than failing the whole /admin/funnel response.
func (h *Handler) adminFunnelJourneyReport(ctx context.Context, days int, excludeArg interface{}) adminFunnelJourney {
	out := adminFunnelJourney{
		Days:        days,
		DarkSegment: "login→back",
	}

	values := make([]int, len(journeyStageSpec))
	dest := make([]interface{}, len(values))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := h.pool.QueryRow(ctx, adminFunnelJourneyQuery(), daysArg(days), excludeArg).Scan(dest...); err != nil {
		log.Printf("admin funnel: read journey: %v", err)
		return out
	}

	out.Stages = make([]journeyStage, 0, len(journeyStageSpec))
	for i, spec := range journeyStageSpec {
		out.Stages = append(out.Stages, journeyStage{
			Key: spec.key, Label: spec.label, Value: values[i], Source: spec.source,
		})
	}

	rows, err := h.pool.Query(ctx, adminFunnelJourneySlicesQuery(), daysArg(days), excludeArg)
	if err != nil {
		log.Printf("admin funnel: read journey slices: %v", err)
		return out
	}
	defer rows.Close()

	byStage := map[string][]journeySlicePart{}
	var order []string
	for rows.Next() {
		var stageKey, key string
		var value int
		if err := rows.Scan(&stageKey, &key, &value); err != nil {
			log.Printf("admin funnel: scan journey slice: %v", err)
			return out
		}
		if _, seen := byStage[stageKey]; !seen {
			order = append(order, stageKey)
		}
		byStage[stageKey] = append(byStage[stageKey], journeySlicePart{Key: key, Value: value})
	}
	if err := rows.Err(); err != nil {
		log.Printf("admin funnel: read journey slices: %v", err)
		return out
	}

	out.Slices = make([]journeySlice, 0, len(order))
	for _, stageKey := range order {
		out.Slices = append(out.Slices, journeySlice{
			StageKey: stageKey,
			Label:    journeySliceLabels[stageKey],
			Parts:    byStage[stageKey],
		})
	}
	return out
}
