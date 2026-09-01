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

// Goal ids on kcFunnelCounterID, in funnel order. Two legs share one host:
// the native email/password form (kcGoal* prefix, "Регистрация: *") and the
// Yandex broker round-trip ("Яндекс: *"), plus the shared login page.
// The leg a visitor takes is their choice on the login page, so the two legs
// are reported side by side, never summed -- a person who tried both counts
// once in each leg and once in Registered.
const (
	kcGoalLoginView               = 601095017
	kcGoalLoginSubmit             = 601095084
	kcGoalNativeRegistrationStart = 601095085
	kcGoalRegisterView            = 598690125
	kcGoalRegisterEmailFilled     = 598690126
	kcGoalRegisterPasswordFilled  = 598690127
	kcGoalRegisterSubmit          = 598690144
	kcGoalRegisterError           = 598690161
	kcGoalYandexStart             = 601042593
	kcGoalYandexRegistrationView  = 601042594
	kcGoalYandexEmailFilled       = 601042595
	kcGoalYandexSubmit            = 601042596
	kcGoalYandexError             = 601042597
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

// adminFunnelKcFunnel is the Keycloak-host registration funnel for
// /admin/funnel: two legs (native form, Yandex broker) plus the shared login
// page, against Registered as ground truth from Postgres. Both legs speak
// goal*users (unique users), not reaches, so stages nest.
//
// Available=false + Note is the failure signal; a Metrika outage must never
// break the rest of the funnel response.
type adminFunnelKcFunnel struct {
	Available bool                   `json:"available"`
	Days      int                    `json:"days"`
	Login     []overviewFunnelStage  `json:"login"`
	Native    []overviewFunnelStage  `json:"native"`
	Yandex    []overviewFunnelStage  `json:"yandex"`
	Errors    []overviewFunnelStage  `json:"errors"`
	Channels  []overviewChannelCount `json:"channels"`
	Note      string                 `json:"note,omitempty"`
}

// adminKcFunnelReport assembles the id.dada-tuda.ru funnel for the window.
// Registered comes straight from Postgres (channels breakdown); stages come
// from the Metrika Stat API. Both Postgres reads are best-effort: their
// failure leaves the count at zero rather than failing the whole response.
func (h *Handler) adminKcFunnelReport(ctx context.Context, days int) adminFunnelKcFunnel {
	out := adminFunnelKcFunnel{
		Days: days,
		Login: []overviewFunnelStage{
			{Key: "kc_login_view", Label: "Открыли вход"},
			{Key: "kc_login_submit", Label: "Отправили логин"},
		},
		Native: []overviewFunnelStage{
			{Key: "kc_native_registration_start", Label: "Кликнули «Регистрация»"},
			{Key: "kc_register_view", Label: "Открыли форму регистрации"},
			{Key: "kc_register_email_filled", Label: "Заполнили e-mail"},
			{Key: "kc_register_password_filled", Label: "Заполнили пароль"},
			{Key: "kc_register_submit", Label: "Отправили форму"},
		},
		Yandex: []overviewFunnelStage{
			{Key: "kc_yandex_start", Label: "Начали вход через Яндекс"},
			{Key: "kc_yandex_registration_view", Label: "Подтвердили профиль"},
			{Key: "kc_yandex_email_filled", Label: "Заполнили e-mail"},
			{Key: "kc_yandex_submit", Label: "Создали аккаунт"},
		},
		Errors: []overviewFunnelStage{
			{Key: "kc_register_error", Label: "Ошибка формы регистрации"},
			{Key: "kc_yandex_error", Label: "Ошибка Яндекс-регистрации"},
		},
	}

	if channels, err := h.overviewRegistrationChannels(ctx, days); err == nil {
		out.Channels = channels
	}

	if h.cfg.MetrikaOAuthToken == "" {
		out.Note = "METRIKA_OAUTH_TOKEN не настроен"
		return out
	}

	legs := []struct {
		goals []int
		stages []overviewFunnelStage
	}{
		{goals: []int{kcGoalLoginView, kcGoalLoginSubmit}, stages: out.Login},
		{goals: []int{kcGoalNativeRegistrationStart, kcGoalRegisterView, kcGoalRegisterEmailFilled,
			kcGoalRegisterPasswordFilled, kcGoalRegisterSubmit}, stages: out.Native},
		{goals: []int{kcGoalYandexStart, kcGoalYandexRegistrationView, kcGoalYandexEmailFilled,
			kcGoalYandexSubmit}, stages: out.Yandex},
		{goals: []int{kcGoalRegisterError, kcGoalYandexError}, stages: out.Errors},
	}

	available := true
	var notes []string
	for _, leg := range legs {
		totals, err := fetchMetrikaGoalUsers(ctx, h.cfg.MetrikaOAuthToken, kcFunnelCounterID, days, leg.goals...)
		if err != nil {
			available = false
			notes = append(notes, err.Error())
			continue
		}
		for i := range leg.stages {
			if i < len(totals) {
				leg.stages[i].Count = int(totals[i])
			}
		}
	}
	out.Available = available
	out.Note = strings.Join(notes, "; ")
	return out
}

// metrikaStatTotals is the subset of the Stat API's response this needs: the
// grand totals, one number per requested metric, same order as the metrics
// list -- the Stat API's "totals" field is a flat array, not per-row nested.
type metrikaStatTotals struct {
	Totals []float64 `json:"totals"`
}

// fetchMetrikaGoalUsers asks the Yandex Metrika Stat API how many unique
// users reached each goal on counterID over the last `days` days, in the
// same order as goalIDs. Uses goal*users, NOT goal*reaches: a funnel stage
// has to be countable against the stage above it, and reaches is not (one
// person firing a goal three times contributes three reaches but one user).
// https://api-metrika.yandex.net/stat/v1/data
func fetchMetrikaGoalUsers(ctx context.Context, oauthToken string, counterID, days int, goalIDs ...int) ([]float64, error) {
	metrics := make([]string, len(goalIDs))
	for i, g := range goalIDs {
		metrics[i] = fmt.Sprintf("ym:s:goal%dusers", g)
	}

	q := url.Values{}
	q.Set("ids", strconv.Itoa(counterID))
	q.Set("metrics", strings.Join(metrics, ","))
	q.Set("date1", strconv.Itoa(days)+"daysAgo")
	q.Set("date2", "today")
	q.Set("accuracy", "full")

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
	if resp.StatusCode >= http.StatusBadRequest {
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
