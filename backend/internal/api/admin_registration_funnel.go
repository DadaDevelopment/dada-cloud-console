package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// kcFunnelCounterID is the dedicated Yandex Metrika counter for
// id.dada-tuda.ru (the Keycloak login/registration host). It is NOT the
// console's shared counter (110158915, frontend/lib/metrika.ts) -- that one
// mixes traffic from several auth-consuming projects and cannot answer "how
// many people opened OUR registration form". Provisioned via the
// YandexMetrikaCounter CR in argo-infra (apps/keycloak/resources.values.yaml).
const kcFunnelCounterID = 111697724

// Goal ids on kcFunnelCounterID, created once via the metrika-instrumentor
// skill's create-metrika-goals.sh against theme-src/theme/dada/login/resources/js/dada.js:
// kcGoalView=kc_register_view, kcGoalEmailFilled=kc_register_email_filled,
// kcGoalPasswordFilled=kc_register_password_filled, kcGoalSubmit=kc_register_submit,
// kcGoalError=kc_register_error. Order matches the funnel stage order below.
const (
	kcGoalView           = 598690125
	kcGoalEmailFilled    = 598690126
	kcGoalPasswordFilled = 598690127
	kcGoalSubmit         = 598690144
	kcGoalError          = 598690161
)

const metrikaStatTimeout = 8 * time.Second

var metrikaStatHTTPClient = &http.Client{Timeout: metrikaStatTimeout}

// overviewFunnelStage is one step of a client-side funnel: a goal name, a
// human label and how many times it fired in the window.
type overviewFunnelStage struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// overviewChannelCount is one signup door (users.signup_channel: 'password'
// or a Keycloak broker alias) and how many rows landed through it in the
// window.
type overviewChannelCount struct {
	Channel string `json:"channel"`
	Count   int    `json:"count"`
}

// overviewRegistrationFunnel is the Keycloak-side counterpart to
// overviewDynamics.Signups: everything between "opened the registration form"
// and "became a row in user_accounts". Registered comes straight from
// Postgres (ground truth); Stages comes from Yandex Metrika because the
// Keycloak theme has no first-party event sink of its own (dada.js only
// calls window.ym, unlike the console's reachGoal+ux_events dual write) --
// see frontend/lib/metrika.ts. A Metrika outage must never break the rest of
// the admin overview, so this never returns an error: Available=false plus
// Note is the failure signal.
//
// Stages is structurally blind to identity-provider (Yandex/google/github)
// signups: a brokered login redirects off the registration form's DOM before
// any reachGoal call fires, so it never touches those goals. Channels closes
// that gap from Postgres, which sees every door regardless of how the row
// was born -- and per argo-infra yandex-idp.yaml, Yandex has been the ONLY
// open signup door since 2026-08-13, so Channels is usually where most of
// Registered actually lives even though Stages' funnel is nearly empty for
// them. Rows older than migration 132 have signup_channel = NULL and are
// dropped from Channels (their door was never recorded), so Channels can
// legitimately sum to less than Registered.
type overviewRegistrationFunnel struct {
	Available  bool                   `json:"available"`
	Days       int                    `json:"days"`
	Registered int                    `json:"registered"`
	Stages     []overviewFunnelStage  `json:"stages"`
	Channels   []overviewChannelCount `json:"channels"`
	Note       string                 `json:"note,omitempty"`
}

func (h *Handler) overviewRegistrationFunnel(ctx context.Context, days int) overviewRegistrationFunnel {
	out := overviewRegistrationFunnel{
		Days: days,
		Stages: []overviewFunnelStage{
			{Key: "kc_register_view", Label: "Открыли регистрацию"},
			{Key: "kc_register_email_filled", Label: "Заполнили e-mail"},
			{Key: "kc_register_password_filled", Label: "Заполнили пароль"},
			{Key: "kc_register_submit", Label: "Отправили форму"},
			{Key: "kc_register_error", Label: "Ошибка при регистрации"},
		},
	}

	if registered, err := h.overviewRegisteredCount(ctx, days); err == nil {
		out.Registered = registered
	}
	if channels, err := h.overviewRegistrationChannels(ctx, days); err == nil {
		out.Channels = channels
	}

	if h.cfg.MetrikaOAuthToken == "" {
		out.Note = "METRIKA_OAUTH_TOKEN не настроен"
		return out
	}

	totals, err := fetchMetrikaGoalReaches(ctx, h.cfg.MetrikaOAuthToken, kcFunnelCounterID, days,
		kcGoalView, kcGoalEmailFilled, kcGoalPasswordFilled, kcGoalSubmit, kcGoalError)
	if err != nil {
		out.Note = "Yandex Metrika недоступна: " + err.Error()
		return out
	}

	out.Available = true
	for i := range out.Stages {
		if i < len(totals) {
			out.Stages[i].Count = int(totals[i])
		}
	}
	return out
}

// overviewRegisteredCount is the real registered-account count for the same
// window the funnel stages cover, so the two are directly comparable instead
// of a Metrika sample eyeballed against an unrelated period.
func (h *Handler) overviewRegisteredCount(ctx context.Context, days int) (int, error) {
	var n int
	err := h.pool.QueryRow(ctx, `
		SELECT count(*) FROM user_accounts
		WHERE created_at >= now() - ($1 || ' days')::interval AND account_kind = $2`,
		strconv.Itoa(days), overviewCustomerKind,
	).Scan(&n)
	return n, err
}

// overviewRegistrationChannels breaks overviewRegisteredCount down by
// users.signup_channel for the same window, so "Yandex signups skip email
// verification and convert higher" is a number, not a guess. NULL channels
// (rows born before migration 132) are excluded rather than lumped into
// 'password', since their door was never recorded.
func (h *Handler) overviewRegistrationChannels(ctx context.Context, days int) ([]overviewChannelCount, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT signup_channel, count(*) FROM user_accounts
		WHERE created_at >= now() - ($1 || ' days')::interval
		    AND account_kind = $2
		    AND signup_channel IS NOT NULL
		GROUP BY signup_channel
		ORDER BY count(*) DESC`,
		strconv.Itoa(days), overviewCustomerKind,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []overviewChannelCount
	for rows.Next() {
		var c overviewChannelCount
		if err := rows.Scan(&c.Channel, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// metrikaStatTotals is the subset of the Stat API's response this needs: the
// grand totals, one number per requested metric, same order as the metrics
// list -- the Stat API's "totals" field is a flat array, not per-row nested.
type metrikaStatTotals struct {
	Totals []float64 `json:"totals"`
}

// fetchMetrikaGoalReaches asks the Yandex Metrika Stat API how many times
// each goal fired on counterID over the last `days` days, in the same order
// as goalIDs. https://api-metrika.yandex.net/stat/v1/data
func fetchMetrikaGoalReaches(ctx context.Context, oauthToken string, counterID, days int, goalIDs ...int) ([]float64, error) {
	metrics := make([]string, len(goalIDs))
	for i, g := range goalIDs {
		metrics[i] = fmt.Sprintf("ym:s:goal%dreaches", g)
	}

	q := url.Values{}
	q.Set("ids", strconv.Itoa(counterID))
	q.Set("metrics", strings.Join(metrics, ","))
	q.Set("date1", strconv.Itoa(days)+"daysAgo")
	q.Set("date2", "today")

	endpoint := metrikaStatAPIURL + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "OAuth "+oauthToken)
	req.Header.Set("Accept", "application/json")

	resp, err := metrikaStatHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed metrikaStatTotals
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if len(parsed.Totals) == 0 {
		return nil, fmt.Errorf("empty totals")
	}
	return parsed.Totals, nil
}
