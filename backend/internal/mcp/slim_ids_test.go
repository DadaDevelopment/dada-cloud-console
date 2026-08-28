package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// getOperationBody is the shape the console returned for the getOperation call
// the owner screenshotted on 2026-08-28: four UUIDs, a git path only the
// gitops-agent can use, and a payload echoing what the caller had just written.
const getOperationBody = `{
  "operation": {
    "id": "1509b5d3-ad60-42eb-986b-04556e2f6f44",
    "actor_id": "0c4ba8b1-1c3a-4c0e-9d0e-3f6f9a3f37cd",
    "project_id": "7a387969-e082-415c-8b61-1f53f7e18295",
    "environment_id": "3b21c6d9-2b52-4a58-9f1e-8e6a9f7e1a44",
    "action": "CreateAgent",
    "resource_kind": "ManagedAgent",
    "resource_name": "tg-roundtrip-test",
    "status": "Committed",
    "payload": {
      "app_name": "tg-roundtrip-test",
      "model_config_id": "ce5e0b41-9d3e-4a2f-9d5b-1f9a6e2c4d77"
    },
    "git_commit": "9f3c1b2a4d5e6f708192a3b4c5d6e7f809a1b2c3",
    "git_path": "apps/agent-sandbox/prod/tg-roundtrip-test/values.yaml",
    "created_at": "2026-08-28T09:12:44Z",
    "updated_at": "2026-08-28T09:12:51Z"
  }
}`

// TestSlimGetOperationDropsTheIdStormAndKeepsTheEvidence pins both poles of the
// trim. The ids go because nothing on this surface takes them: the caller wrote
// the operation id itself, and a project, an environment and an actor are not
// addressable by uuid here. git_commit stays because it is the only proof the
// write actually landed in git.
func TestSlimGetOperationDropsTheIdStormAndKeepsTheEvidence(t *testing.T) {
	op := slimmedRecord(t, "getOperation", getOperationBody, "operation")

	for _, gone := range []string{"id", "actor_id", "project_id", "environment_id", "git_path"} {
		if _, present := op[gone]; present {
			t.Errorf("operation still carries %q — the caller cannot address anything with it", gone)
		}
	}
	for _, kept := range []string{"action", "resource_kind", "resource_name", "status", "payload", "git_commit", "created_at", "updated_at"} {
		if _, present := op[kept]; !present {
			t.Errorf("operation lost %q, which is what the answer was for", kept)
		}
	}
}

// TestSlimGetOperationLeavesAnIdInsideThePayloadAlone holds the line between an
// echo and an address. model_config_id names something the caller did not ask
// about and may still need to reach; stripping every key that ends in _id would
// take it too.
func TestSlimGetOperationLeavesAnIdInsideThePayloadAlone(t *testing.T) {
	op := slimmedRecord(t, "getOperation", getOperationBody, "operation")

	payload, ok := op["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %v, want the object the operation was created with", op["payload"])
	}
	if payload["model_config_id"] != "ce5e0b41-9d3e-4a2f-9d5b-1f9a6e2c4d77" {
		t.Errorf("payload.model_config_id = %v, want the id preserved — it addresses something the caller did not name",
			payload["model_config_id"])
	}
}

// getBuildBody is the console's build record: the same two address ids, plus
// the git_repo_id and the build id the caller passed as buildId.
const getBuildBody = `{
  "build": {
    "id": "5a2f1e77-3c4d-4b9a-8e1f-7d6c5b4a3928",
    "git_repo_id": "b1c2d3e4-f5a6-4b7c-8d9e-0f1a2b3c4d5e",
    "environment_id": "3b21c6d9-2b52-4a58-9f1e-8e6a9f7e1a44",
    "app_name": "lead-poc",
    "status": "success",
    "trigger": "manual",
    "commit_sha": "9f3c1b2a4d5e6f708192a3b4c5d6e7f809a1b2c3",
    "commit_message": "wire the intake form",
    "branch": "main",
    "image_uri": "registry.dada.tuda/lead-poc:9f3c1b2",
    "started_at": "2026-08-28T08:40:00Z",
    "finished_at": "2026-08-28T08:43:12Z",
    "created_at": "2026-08-28T08:39:58Z",
    "source": "github"
  }
}`

// TestSlimGetBuildKeepsWhatAnswersDidItShip is the getBuild half of the same
// complaint. The fields kept are the ones the owner listed in ans.json: what
// was built, from which commit, into which image, and whether it worked.
func TestSlimGetBuildKeepsWhatAnswersDidItShip(t *testing.T) {
	build := slimmedRecord(t, "getBuild", getBuildBody, "build")

	for _, gone := range []string{"id", "git_repo_id", "environment_id"} {
		if _, present := build[gone]; present {
			t.Errorf("build still carries %q", gone)
		}
	}
	for _, kept := range []string{"app_name", "status", "trigger", "commit_sha", "commit_message", "branch", "image_uri", "started_at", "finished_at", "source"} {
		if _, present := build[kept]; !present {
			t.Errorf("build lost %q, which is what tells the caller whether the change shipped", kept)
		}
	}
}

// TestSlimResponseStripsEchoIdsFromAToolWithNoSlimmer proves the id rule is
// global rather than a per-tool list that a new endpoint would escape.
func TestSlimResponseStripsEchoIdsFromAToolWithNoSlimmer(t *testing.T) {
	const body = `{"databases":[{"name":"leads","project_id":"7a387969-e082-415c-8b61-1f53f7e18295","engine":"postgres"}]}`
	out := slimResponse("listDatabases", []byte(body))

	if strings.Contains(string(out), "project_id") {
		t.Errorf("a tool with no slimmer still echoed project_id: %s", out)
	}
	if !strings.Contains(string(out), `"engine":"postgres"`) {
		t.Errorf("the row lost its own fields: %s", out)
	}
}

// TestSlimResponseDoesNotDriftLargeNumbers guards the re-marshal: a body is
// decoded and written out again, and a float round-trip would turn a byte count
// into 1e+06.
func TestSlimResponseDoesNotDriftLargeNumbers(t *testing.T) {
	const body = `{"build":{"id":"x","bytes":1000000}}`
	out := slimResponse("getBuild", []byte(body))
	if !strings.Contains(string(out), "1000000") {
		t.Errorf("number drifted through the slimmer: %s", out)
	}
}

// slimmedRecord runs a body through slimResponse and returns the record inside
// the named envelope.
func slimmedRecord(t *testing.T, tool, body, envelope string) map[string]any {
	t.Helper()
	out := slimResponse(tool, []byte(body))

	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("slimmed %s body is not JSON: %v\n%s", tool, err, out)
	}
	record, ok := doc[envelope].(map[string]any)
	if !ok {
		t.Fatalf("slimmed %s body lost its %q envelope: %s", tool, envelope, out)
	}
	t.Logf("%s: %d bytes in, %d bytes out", tool, len(body), len(out))
	return record
}
