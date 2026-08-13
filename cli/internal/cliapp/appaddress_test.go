package cliapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dada-tuda/console/cli/internal/apiclient"
)

// addressServer answers the two endpoints the address poll uses: the app list
// (whose snapshot carries "url" only once the app is live) and the hostname
// list (which the platform fills the moment the app is created).
func addressServer(t *testing.T, snapshotURL, hostname string, managed bool) (*apiclient.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/hostnames"):
			out := map[string]any{"hostnames": []map[string]any{}}
			if hostname != "" {
				out["hostnames"] = []map[string]any{
					{"hostname": hostname, "managed": managed, "status": "pending"},
				}
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			app := map[string]any{"name": "genagent", "summary_json": map[string]any{}}
			if snapshotURL != "" {
				app["summary_json"] = map[string]any{"url": snapshotURL}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"apps": []any{app}})
		}
	}))
	return apiclient.New(srv.URL, srv.Client(), nil, ""), srv
}

func shortPoll(t *testing.T) {
	t.Helper()
	interval, timeout := urlPollInterval, urlPollTimeout
	urlPollInterval, urlPollTimeout = time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { urlPollInterval, urlPollTimeout = interval, timeout })
}

// TestAddressComesFromTheLiveSnapshotWhenThePlatformHasIt keeps the happy path:
// a confirmed URL is reported as ready, with no hedging.
func TestAddressComesFromTheLiveSnapshotWhenThePlatformHasIt(t *testing.T) {
	shortPoll(t)
	c, srv := addressServer(t, "https://genagent-797c82.dada-tuda.ru", "", false)
	defer srv.Close()

	addr, err := pollAppAddress(context.Background(), c, "p", "e", "genagent")
	if err != nil {
		t.Fatal(err)
	}
	if !addr.live || addr.url != "https://genagent-797c82.dada-tuda.ru" {
		t.Fatalf("got %+v", addr)
	}
}

// TestAddressFallsBackToTheHostnameThePlatformAlreadyMinted is the regression
// lock for the deploy of 2026-08-13 21:52: the surrogate name existed in
// domain_hostnames 30 seconds after the build, and ddc still finished with
// "адрес ещё не назначен" because the snapshot url waits on a running pod.
func TestAddressFallsBackToTheHostnameThePlatformAlreadyMinted(t *testing.T) {
	shortPoll(t)
	c, srv := addressServer(t, "", "genagent-797c82.dada-tuda.ru", true)
	defer srv.Close()

	addr, err := pollAppAddress(context.Background(), c, "p", "e", "genagent")
	if err != nil {
		t.Fatal(err)
	}
	if addr.url != "https://genagent-797c82.dada-tuda.ru" {
		t.Fatalf("got %+v", addr)
	}
	if addr.live {
		t.Fatal("an address the platform has not confirmed must not be reported as ready")
	}
}

// TestAppWithNoHostnameYetStillEndsCleanly keeps the old wording alive for the
// case it was written for: nothing is known, so nothing is promised.
func TestAppWithNoHostnameYetStillEndsCleanly(t *testing.T) {
	shortPoll(t)
	c, srv := addressServer(t, "", "", false)
	defer srv.Close()

	addr, err := pollAppAddress(context.Background(), c, "p", "e", "genagent")
	if err != nil {
		t.Fatal(err)
	}
	if addr.url != "" || addr.live {
		t.Fatalf("got %+v", addr)
	}
}

// TestAttachedDomainWinsOverTheSurrogate pins the choice a user would make:
// once they attach their own domain, that is the address they want printed.
func TestAttachedDomainWinsOverTheSurrogate(t *testing.T) {
	shortPoll(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/hostnames") {
			_ = json.NewEncoder(w).Encode(map[string]any{"hostnames": []map[string]any{
				{"hostname": "genagent-797c82.dada-tuda.ru", "managed": true},
				{"hostname": "rod.example.com", "managed": false},
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"apps": []any{}})
	}))
	defer srv.Close()
	c := apiclient.New(srv.URL, srv.Client(), nil, "")

	addr, err := pollAppAddress(context.Background(), c, "p", "e", "genagent")
	if err != nil {
		t.Fatal(err)
	}
	if addr.url != "https://rod.example.com" {
		t.Fatalf("got %+v", addr)
	}
}
