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

// overviewRegistrationFunnel is the Keycloak-side counterpart to
// overviewDynamics.Signups: everything between "opened the registration form"
// and "became a row in user_accounts". Registered comes straight from
// Postgres (ground truth); Stages comes from Yandex Metrika because the
// Keycloak theme has no first-party event sink of its own (dada.js only
// calls window.ym, unlike the console's reachGoal+ux_events dual write) --
// see frontend/lib/metrika.ts. A Metrika outage must never break the rest of
// the admin overview, so this never returns an error: Available=false plus
// Note is the failure signal.
type overviewRegistrationFunnel struct {
	Available  bool                  `json:"available"`
	Days       int                   `json:"days"`
	Registered int                   `json:"registered"`
	Stages     []overviewFunnelStage `json:"stages"`
	Note       string                `json:"note,omitempty"`
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

// metrikaStatTotals is the subset of the Stat API's response this needs: the
// grand totals row, in the same order as the requested metrics.
type metrikaStatTotals struct {
	Totals [][]float64 `json:"totals"`
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

	endpoint := "https://api-metrika.yandex.net/stat/v1/data?" + q.Encode()
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
	return parsed.Totals[0], nil
}
