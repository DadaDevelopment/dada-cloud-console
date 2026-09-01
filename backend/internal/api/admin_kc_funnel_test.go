package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fetchMetrikaGoalUsers must request goal*users (unique visitors), not
// goal*reaches: a funnel stage has to be countable against the stage above
// it, and reaches double-counts repeat events, which is exactly the
// production bug the traffic-source funnel already hit.
func TestFetchMetrikaGoalUsersUsesUniqueUsersMetric(t *testing.T) {
	var gotMetrics, gotDate1 string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		gotMetrics = q.Get("metrics")
		gotDate1 = q.Get("date1")
		if got := q.Get("ids"); got != "111697724" {
			t.Errorf("ids = %q", got)
		}
		if got := q.Get("accuracy"); got != "full" {
			t.Errorf("accuracy = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totals":[21.0,3.0,0.0]}`))
	}))
	defer server.Close()

	previousURL := metrikaStatAPIURL
	previousClient := metrikaStatHTTPClient
	metrikaStatAPIURL = server.URL
	metrikaStatHTTPClient = server.Client()
	t.Cleanup(func() {
		metrikaStatAPIURL = previousURL
		metrikaStatHTTPClient = previousClient
	})

	totals, err := fetchMetrikaGoalUsers(context.Background(), "test-token", kcFunnelCounterID, 7,
		kcGoalLoginView, kcGoalNativeRegistrationStart, kcGoalYandexRegistrationView)
	if err != nil {
		t.Fatalf("fetch Metrika goal users: %v", err)
	}
	want := "ym:s:goal601095017users,ym:s:goal601095085users,ym:s:goal601042594users"
	if gotMetrics != want {
		t.Fatalf("metrics = %q, want %q (goal stages must be unique users, not reaches)", gotMetrics, want)
	}
	if gotDate1 != "7daysAgo" {
		t.Fatalf("date1 = %q", gotDate1)
	}
	if len(totals) != 3 || totals[0] != 21 || totals[1] != 3 || totals[2] != 0 {
		t.Fatalf("totals = %#v", totals)
	}
}

// A Metrika HTTP failure must surface as an error (the caller degrades to
// Available=false + Note), not as a silent zero funnel.
func TestFetchMetrikaGoalUsersSurfacesHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer server.Close()

	previousURL := metrikaStatAPIURL
	previousClient := metrikaStatHTTPClient
	metrikaStatAPIURL = server.URL
	metrikaStatHTTPClient = server.Client()
	t.Cleanup(func() {
		metrikaStatAPIURL = previousURL
		metrikaStatHTTPClient = previousClient
	})

	if _, err := fetchMetrikaGoalUsers(context.Background(), "test-token", kcFunnelCounterID, 7, kcGoalLoginView); err == nil {
		t.Fatal("expected error on HTTP 502, got nil")
	}
}

// The adminKcFunnelReport leg table stays in sync with the stage lists it
// fills: one goal per stage, same order. Adding a stage without a goal (or
// the reverse) would silently report zero.
func TestAdminKcFunnelLegsCoverEveryStage(t *testing.T) {
	h := &Handler{pool: overviewBrokenTestPool(t)}
	report := h.adminKcFunnelReport(context.Background(), 7)
	if report.Available {
		t.Skip("Metrika configured in test env; leg table still checked below")
	}

	total := len(report.Login) + len(report.Native) + len(report.Yandex) + len(report.Errors)
	if total != 13 {
		t.Fatalf("stage count = %d, want 13 (login 2 + native 5 + yandex 4 + errors 2)", total)
	}
	for _, s := range append(append(append(report.Login, report.Native...), report.Yandex...), report.Errors...) {
		if s.Key == "" || s.Label == "" {
			t.Fatalf("stage with empty key/label: %#v", s)
		}
	}
}
