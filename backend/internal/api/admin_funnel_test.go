package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminFunnelQueriesMatchCurrentSchema(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	ctx := context.Background()

	var counts [8]int
	err := pool.QueryRow(ctx, adminFunnelCountsQuery("TRUE"), nil).Scan(
		&counts[0], &counts[1], &counts[2], &counts[3],
		&counts[4], &counts[5], &counts[6], &counts[7],
	)
	if err != nil {
		t.Fatalf("admin funnel counts query must compile against current schema: %v", err)
	}

	rows, err := pool.Query(ctx, adminFunnelCohortsQuery("TRUE"))
	if err != nil {
		t.Fatalf("admin funnel cohorts query must compile against current schema: %v", err)
	}
	rows.Close()
}

func TestFetchMetrikaTrafficSourceFunnel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "OAuth test-token" {
			t.Fatalf("authorization = %q", got)
		}
		q := r.URL.Query()
		if got := q.Get("ids"); got != "110158915" {
			t.Fatalf("ids = %q", got)
		}
		if got := q.Get("dimensions"); got != "ym:s:trafficSource" {
			t.Fatalf("dimensions = %q", got)
		}
		if got := q.Get("date1"); got != "30daysAgo" {
			t.Fatalf("date1 = %q", got)
		}
		wantMetrics := "ym:s:visits,ym:s:users," +
			"ym:s:goal585010094users,ym:s:goal593177849users," +
			"ym:s:goal586052031users,ym:s:goal585205874users"
		if gotMetrics := q.Get("metrics"); gotMetrics != wantMetrics {
			t.Fatalf("metrics = %q, want %q (goal stages must be unique users, not reaches)", gotMetrics, wantMetrics)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data":[{"dimensions":[{"name":"Direct traffic"}],"metrics":[12,9,5,3,2,1]}],
			"totals":[12,9,5,3,2,1]
		}`))
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

	channels, totals, err := fetchMetrikaTrafficSourceFunnel(context.Background(), "test-token", 30)
	if err != nil {
		t.Fatalf("fetch Metrika traffic-source funnel: %v", err)
	}
	if len(channels) != 1 || channels[0].Source != "Direct traffic" || channels[0].RegistrationComplete != 2 {
		t.Fatalf("channels = %#v", channels)
	}
	if totals.Visits != 12 || totals.DeploySuccess != 1 {
		t.Fatalf("totals = %#v", totals)
	}
}
