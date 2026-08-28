package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

const getProjectBody = `{
  "project": {
    "id": "7a387969-e082-415c-8b61-1f53f7e18295",
    "name": "agent-sandbox",
    "display_name": "agent-sandbox",
    "owner_type": "team",
    "owner_id": "5d3a2f11-1111-2222-3333-444455556666",
    "org_id": "dada",
    "default_environment": "prod",
    "quotas": {"cpu": "8", "memory": "16Gi"},
    "created_at": "2026-08-10T21:00:40.566351Z",
    "updated_at": "2026-08-10T21:00:40.566351Z"
  },
  "role": "Owner",
  "environments": [
    {
      "id": "8792d2e5-4961-4098-8d69-d6f0bde7ceba",
      "project_id": "7a387969-e082-415c-8b61-1f53f7e18295",
      "name": "box-727369df",
      "namespace": "agent-sandbox-box-727369df",
      "type": "dev",
      "runtime": "box",
      "limit_range": {},
      "resource_quota": {},
      "is_ephemeral": false,
      "created_at": "2026-08-10T21:00:40.566351Z",
      "updated_at": "2026-08-10T21:00:40.566351Z"
    },
    {
      "id": "eff93bb8-bdd5-4145-8bd5-d3e9cb1faf27",
      "project_id": "7a387969-e082-415c-8b61-1f53f7e18295",
      "name": "prod",
      "namespace": "agent-sandbox-prod",
      "type": "prod",
      "runtime": "k8s",
      "limit_range": {},
      "resource_quota": {},
      "is_ephemeral": false,
      "created_at": "2026-08-10T21:00:40.566351Z",
      "updated_at": "2026-08-10T21:00:40.566351Z"
    },
    {
      "id": "a8747011-66f7-4891-9728-5889ab1fc7cf",
      "project_id": "7a387969-e082-415c-8b61-1f53f7e18295",
      "name": "ephprobe1",
      "namespace": "agent-sandbox-ephprobe1",
      "type": "dev",
      "runtime": "k8s",
      "limit_range": {},
      "resource_quota": {},
      "is_ephemeral": true,
      "expires_at": "2026-08-29T07:27:19.875749Z",
      "created_at": "2026-08-11T07:27:19.875749Z",
      "updated_at": "2026-08-11T07:27:19.875749Z"
    }
  ]
}`

func TestSlimGetProjectIsAMapToTheSubresource(t *testing.T) {
	out := slimResponse("getProject", []byte(getProjectBody))

	var doc struct {
		Project      map[string]any   `json:"project"`
		Role         string           `json:"role"`
		Environments []map[string]any `json:"environments"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("slimmed body is not JSON: %v\n%s", err, out)
	}

	for _, gone := range []string{"quotas", "owner_id", "owner_type", "created_at", "updated_at", "display_name"} {
		if _, present := doc.Project[gone]; present {
			t.Errorf("project still carries %q: getProject is the step before a subresource, the details belong to it", gone)
		}
	}
	if doc.Project["name"] != "agent-sandbox" || doc.Project["default_environment"] != "prod" {
		t.Errorf("project header lost its address: %v", doc.Project)
	}
	if doc.Role != "Owner" {
		t.Errorf("role = %q, want the caller's role kept", doc.Role)
	}

	if len(doc.Environments) != 3 {
		t.Fatalf("environments = %d, want all 3 -- slimming trims fields, never rows", len(doc.Environments))
	}
	byName := map[string]map[string]any{}
	for _, e := range doc.Environments {
		name, _ := e["name"].(string)
		byName[name] = e
	}
	if got := byName["prod"]; len(got) != 1 {
		t.Errorf("prod = %v, want a name and nothing else: namespace, type, empty quota blobs and both timestamps are the console's grid", got)
	}
	if got := byName["box-727369df"]; got["runtime"] != "box" {
		t.Errorf("box environment lost its runtime (%v), which is what decides whether the box tools apply at all", got)
	}
	if got := byName["ephprobe1"]; got["is_ephemeral"] != true || got["expires_at"] == nil {
		t.Errorf("ephemeral environment = %v, want the fact that it disappears kept", got)
	}
	if strings.Contains(string(out), "7a387969-e082-415c-8b61-1f53f7e18295\",\"name\":\"box") {
		t.Error("project_id is repeated on the environment rows")
	}
}

// TestSlimGetProjectLeavesAnUnknownShapeAlone holds the rule the whole file
// rests on: a body this code does not recognize is returned byte for byte
// rather than silently truncated.
func TestSlimGetProjectLeavesAnUnknownShapeAlone(t *testing.T) {
	const body = `{"project":{"name":"x"}}`
	if got := string(slimResponse("getProject", []byte(body))); got != body {
		t.Errorf("a body with no environments was rewritten to %s", got)
	}
}
