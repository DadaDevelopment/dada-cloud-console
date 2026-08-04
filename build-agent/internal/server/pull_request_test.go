package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPullRequestEventDecodesTeardownFieldsOnly locks the shrunken payload. The
// event carries the PR number and its repository and nothing else: no head sha,
// no head/base repo, no labels. Those fields fed the creation path, and a struct
// that cannot decode them is a struct nobody can rebuild a preview deploy from
// by accident.
func TestPullRequestEventDecodesTeardownFieldsOnly(t *testing.T) {
	body := []byte(`{
		"action": "closed",
		"number": 42,
		"repository": {"full_name": "acme/webapp"},
		"label": {"name": "preview"},
		"pull_request": {
			"title": "Add pricing page",
			"labels": [{"name": "preview"}],
			"head": {"sha": "abc123", "ref": "feat", "repo": {"full_name": "acme/webapp"}},
			"base": {"repo": {"full_name": "acme/webapp"}}
		}
	}`)

	var ev pullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Action != "closed" || ev.Number != 42 || ev.Repository.FullName != "acme/webapp" {
		t.Fatalf("teardown fields lost: %+v", ev)
	}

	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, gone := range []string{"sha", "labels", "head", "base"} {
		if strings.Contains(string(raw), gone) {
			t.Errorf("pullRequestEvent still carries %q: %s", gone, raw)
		}
	}
}

// TestPullRequestWebhookIsTeardownOnly is the regression proper for removing
// previews as a feature: every action that used to CREATE or refresh a preview
// environment must now be dropped before the handler even looks at the
// database, while "closed" still goes looking for a legacy environment to tear
// down.
//
// The database is deliberately unreachable, which is what makes the two cases
// tell each other apart: a delivery that reaches the DB fails loudly (500), a
// delivery that is dropped first cannot (200). An "opened" that starts
// resolving repos again would flip to 500 and fail this test.
func TestPullRequestWebhookIsTeardownOnly(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none")
	if err != nil {
		t.Fatalf("build unreachable pool: %v", err)
	}
	defer pool.Close()
	s := &Server{pool: pool}

	cases := []struct {
		action string
		want   int
	}{
		{"opened", http.StatusOK},
		{"reopened", http.StatusOK},
		{"synchronize", http.StatusOK},
		{"labeled", http.StatusOK},
		{"unlabeled", http.StatusOK},
		{"assigned", http.StatusOK},
		{"closed", http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			body := []byte(`{"action":"` + tc.action + `","number":7,"repository":{"full_name":"acme/webapp"}}`)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(string(body)))
			s.handlePullRequestWebhook(rec, req, body)
			if rec.Code != tc.want {
				t.Fatalf("action %s: status %d, want %d", tc.action, rec.Code, tc.want)
			}
		})
	}
}
