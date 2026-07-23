package server

import (
	"encoding/json"
	"testing"
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
	body := []byte(`{"action": "labeled", "number": 1, "repository": {"full_name": "acme/webapp"}}`)
	var ev pullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	switch ev.Action {
	case "opened", "reopened", "synchronize", "closed":
		t.Errorf("action %q should not match the handled set", ev.Action)
	}
}
