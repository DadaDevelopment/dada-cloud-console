package server

import (
	"encoding/json"
	"testing"

	"github.com/dada-tuda/console/build-agent/internal/config"
)

func TestPullRequestEventUnmarshalOpened(t *testing.T) {
	body := []byte(`{
		"action": "opened",
		"number": 42,
		"repository": {"full_name": "acme/webapp"},
		"pull_request": {
			"title": "Add pricing page",
			"head": {
				"sha": "abc123",
				"ref": "feature/pricing-page",
				"repo": {"full_name": "acme/webapp"}
			},
			"base": {
				"repo": {"full_name": "acme/webapp"}
			}
		}
	}`)

	var ev pullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Action != "opened" {
		t.Errorf("Action = %q, want opened", ev.Action)
	}
	if ev.Number != 42 {
		t.Errorf("Number = %d, want 42", ev.Number)
	}
	if ev.Repository.FullName != "acme/webapp" {
		t.Errorf("Repository.FullName = %q, want acme/webapp", ev.Repository.FullName)
	}
	if ev.PullRequest.Head.SHA != "abc123" {
		t.Errorf("Head.SHA = %q, want abc123", ev.PullRequest.Head.SHA)
	}
	if ev.PullRequest.Head.Ref != "feature/pricing-page" {
		t.Errorf("Head.Ref = %q, want feature/pricing-page", ev.PullRequest.Head.Ref)
	}
	if ev.PullRequest.Head.Repo.FullName != ev.PullRequest.Base.Repo.FullName {
		t.Errorf("same-repo PR should have equal head/base full_name")
	}
}

func TestPullRequestEventUnmarshalForkPR(t *testing.T) {
	body := []byte(`{
		"action": "synchronize",
		"number": 7,
		"repository": {"full_name": "acme/webapp"},
		"pull_request": {
			"title": "Fix typo",
			"head": {
				"sha": "def456",
				"ref": "fix-typo",
				"repo": {"full_name": "contributor/webapp"}
			},
			"base": {
				"repo": {"full_name": "acme/webapp"}
			}
		}
	}`)

	var ev pullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	headRepo := ev.PullRequest.Head.Repo.FullName
	baseRepo := ev.PullRequest.Base.Repo.FullName
	if headRepo == baseRepo {
		t.Fatalf("fixture should be a fork PR: head=%q base=%q", headRepo, baseRepo)
	}
	if headRepo != "contributor/webapp" {
		t.Errorf("Head.Repo.FullName = %q, want contributor/webapp", headRepo)
	}
}

func TestPullRequestEventUnmarshalClosed(t *testing.T) {
	body := []byte(`{
		"action": "closed",
		"number": 42,
		"repository": {"full_name": "acme/webapp"},
		"pull_request": {
			"title": "Add pricing page",
			"head": {"sha": "abc123", "ref": "feature/pricing-page", "repo": {"full_name": "acme/webapp"}},
			"base": {"repo": {"full_name": "acme/webapp"}}
		}
	}`)

	var ev pullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Action != "closed" {
		t.Errorf("Action = %q, want closed", ev.Action)
	}
	if ev.Number != 42 {
		t.Errorf("Number = %d, want 42", ev.Number)
	}
}

func TestPullRequestEventUnknownActionIgnored(t *testing.T) {
	body := []byte(`{"action": "assigned", "number": 1, "repository": {"full_name": "acme/webapp"}}`)
	var ev pullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	switch ev.Action {
	case "opened", "reopened", "synchronize", "closed", "labeled", "unlabeled":
		t.Errorf("action %q should not match the handled set", ev.Action)
	}
}

// TestPullRequestEventUnmarshalLabels proves both label shapes decode: the PR's
// full current set (which the opt-in check reads) and the single changed label
// GitHub attaches to a labeled/unlabeled delivery.
func TestPullRequestEventUnmarshalLabels(t *testing.T) {
	body := []byte(`{
		"action": "labeled",
		"number": 9,
		"repository": {"full_name": "acme/webapp"},
		"label": {"name": "preview"},
		"pull_request": {
			"title": "Add pricing page",
			"labels": [{"name": "enhancement"}, {"name": "preview"}],
			"head": {"sha": "abc123", "ref": "feat", "repo": {"full_name": "acme/webapp"}},
			"base": {"repo": {"full_name": "acme/webapp"}}
		}
	}`)

	var ev pullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Label.Name != "preview" {
		t.Errorf("Label.Name = %q, want preview", ev.Label.Name)
	}
	if len(ev.PullRequest.Labels) != 2 {
		t.Fatalf("Labels len = %d, want 2", len(ev.PullRequest.Labels))
	}
	if !prHasLabel(&ev, "preview") {
		t.Error("prHasLabel should find the preview label in the PR's set")
	}
	if prHasLabel(&ev, "bug") {
		t.Error("prHasLabel should not find a label the PR does not carry")
	}
}

// TestPreviewOptInRequiresLabel locks the opt-in default: with
// PreviewEnvsRequireLabel on, only a PR carrying the label may create a preview,
// so an ignored PR never spawns an environment nobody asked for. With the flag
// off, every PR opts in (the pre-opt-in behavior, kept as an env kill switch).
func TestPreviewOptInRequiresLabel(t *testing.T) {
	labelled := func(names ...string) *pullRequestEvent {
		ev := &pullRequestEvent{}
		for _, n := range names {
			ev.PullRequest.Labels = append(ev.PullRequest.Labels, struct {
				Name string `json:"name"`
			}{Name: n})
		}
		return ev
	}
	gated := &config.Config{PreviewEnvsRequireLabel: true, PreviewEnvLabel: "preview"}
	open := &config.Config{PreviewEnvsRequireLabel: false, PreviewEnvLabel: "preview"}

	cases := []struct {
		name string
		cfg  *config.Config
		ev   *pullRequestEvent
		want bool
	}{
		{"gated, no labels at all", gated, labelled(), false},
		{"gated, only unrelated labels", gated, labelled("bug", "enhancement"), false},
		{"gated, exact label", gated, labelled("preview"), true},
		{"gated, label among others", gated, labelled("bug", "preview"), true},
		{"gated, case-insensitive", gated, labelled("Preview"), true},
		{"gated, padded label", gated, labelled("  preview "), true},
		{"gated, near-miss label does not count", gated, labelled("previews"), false},
		{"flag off, no labels still opts in", open, labelled(), true},
		{"nil config opts in", nil, labelled(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := previewOptIn(tc.cfg, tc.ev); got != tc.want {
				t.Fatalf("previewOptIn = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPreviewOptedOutOnLabelRemoval proves removing the opt-in label tears the
// preview down immediately, while removing any OTHER label leaves a running
// preview alone -- an unrelated label edit must not destroy someone's env.
func TestPreviewOptedOutOnLabelRemoval(t *testing.T) {
	event := func(action, removed string, remaining ...string) *pullRequestEvent {
		ev := &pullRequestEvent{Action: action}
		ev.Label.Name = removed
		for _, n := range remaining {
			ev.PullRequest.Labels = append(ev.PullRequest.Labels, struct {
				Name string `json:"name"`
			}{Name: n})
		}
		return ev
	}
	gated := &Server{cfg: &config.Config{PreviewEnvsRequireLabel: true, PreviewEnvLabel: "preview"}}
	open := &Server{cfg: &config.Config{PreviewEnvsRequireLabel: false, PreviewEnvLabel: "preview"}}

	cases := []struct {
		name string
		srv  *Server
		ev   *pullRequestEvent
		want bool
	}{
		{"opt-in label removed", gated, event("unlabeled", "preview"), true},
		{"opt-in label removed, case-insensitive", gated, event("unlabeled", "Preview"), true},
		{"unrelated label removed, preview kept", gated, event("unlabeled", "bug", "preview"), false},
		{"unrelated label removed, never had preview", gated, event("unlabeled", "bug"), false},
		{"labeled action is not an opt-out", gated, event("labeled", "preview", "preview"), false},
		{"flag off never opts out", open, event("unlabeled", "preview"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.srv.previewOptedOut(tc.ev); got != tc.want {
				t.Fatalf("previewOptedOut = %v, want %v", got, tc.want)
			}
		})
	}
}
