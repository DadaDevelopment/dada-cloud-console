package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// setEnvVarTool mirrors the shape the generator produces for the write that
// destroyed telemost-bot's configuration: three path parameters, all UUIDs.
func setEnvVarTool() GeneratedTool {
	return GeneratedTool{
		Name:         "setEnvVar",
		Method:       http.MethodPut,
		PathTemplate: "/projects/{projectId}/environments/{envId}/apps/{appName}/env/{key}",
		PathParams:   []string{"projectId", "envId", "appName", "key"},
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"projectId": map[string]any{"type": "string"}},
			"required":   []string{"appName", "envId", "key", "projectId"},
		},
	}
}

// resolveStub answers /resolve the way the backend does, and records what it
// was asked, so a test can prove the name lookup happened rather than infer it.
func resolveStub(t *testing.T, body map[string]any) (*httptest.Server, *[]string) {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/resolve" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		asked = append(asked, r.URL.Query().Get("ref"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

func TestResolveAddressArgs_TurnsANameAddressIntoIds(t *testing.T) {
	srv, asked := resolveStub(t, map[string]any{
		"project":     map[string]any{"id": "11111111-1111-1111-1111-111111111111", "name": "internal"},
		"environment": map[string]any{"id": "22222222-2222-2222-2222-222222222222", "name": "prod"},
	})

	args := map[string]any{"ref": "internal/prod/telemost-bot", "key": "PGHOST", "value": "db"}
	if msg := resolveAddressArgs(context.Background(), setEnvVarTool(), args, srv.URL, "/api/v1"); msg != "" {
		t.Fatalf("resolve failed: %s", msg)
	}

	if got := args["projectId"]; got != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("projectId = %v, want the resolved project id", got)
	}
	if got := args["envId"]; got != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("envId = %v, want the resolved environment id", got)
	}
	if got := args["appName"]; got != "telemost-bot" {
		t.Errorf("appName = %v, want telemost-bot", got)
	}
	if len(*asked) != 1 || (*asked)[0] != "internal/prod" {
		t.Errorf("resolve asked for %v, want one lookup of internal/prod", *asked)
	}
	for _, alias := range []string{"ref", "project", "env", "app"} {
		if _, present := args[alias]; present {
			t.Errorf("alias %q survived into the request arguments and would be sent as a body field", alias)
		}
	}
}

func TestResolveAddressArgs_AcceptsSeparateNameArguments(t *testing.T) {
	srv, _ := resolveStub(t, map[string]any{
		"project":     map[string]any{"id": "11111111-1111-1111-1111-111111111111", "name": "internal"},
		"environment": map[string]any{"id": "22222222-2222-2222-2222-222222222222", "name": "prod"},
	})

	args := map[string]any{"project": "internal", "env": "prod", "app": "telemost-bot", "key": "PGHOST"}
	if msg := resolveAddressArgs(context.Background(), setEnvVarTool(), args, srv.URL, "/api/v1"); msg != "" {
		t.Fatalf("resolve failed: %s", msg)
	}
	if args["projectId"] == nil || args["envId"] == nil || args["appName"] != "telemost-bot" {
		t.Fatalf("named arguments did not fill the path parameters: %v", args)
	}
}

func TestResolveAddressArgs_LeavesUUIDsAloneAndDoesNotCallResolve(t *testing.T) {
	srv, asked := resolveStub(t, map[string]any{})

	args := map[string]any{
		"projectId": "11111111-1111-1111-1111-111111111111",
		"envId":     "22222222-2222-2222-2222-222222222222",
		"appName":   "telemost-bot",
		"key":       "PGHOST",
	}
	if msg := resolveAddressArgs(context.Background(), setEnvVarTool(), args, srv.URL, "/api/v1"); msg != "" {
		t.Fatalf("resolve failed: %s", msg)
	}
	if len(*asked) != 0 {
		t.Errorf("a fully addressed call still paid for a resolve: %v", *asked)
	}
}

func TestResolveAddressArgs_OmittedEnvIsTakenOnlyWhenThereIsExactlyOne(t *testing.T) {
	single, _ := resolveStub(t, map[string]any{
		"project":      map[string]any{"id": "11111111-1111-1111-1111-111111111111", "name": "solo"},
		"environments": []map[string]any{{"id": "33333333-3333-3333-3333-333333333333", "name": "prod"}},
	})
	args := map[string]any{"project": "solo", "app": "bot", "key": "K"}
	if msg := resolveAddressArgs(context.Background(), setEnvVarTool(), args, single.URL, "/api/v1"); msg != "" {
		t.Fatalf("single-environment project should not need an env name: %s", msg)
	}
	if args["envId"] != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("envId = %v, want the project's only environment", args["envId"])
	}

	many, _ := resolveStub(t, map[string]any{
		"project": map[string]any{"id": "11111111-1111-1111-1111-111111111111", "name": "internal"},
		"environments": []map[string]any{
			{"id": "44444444-4444-4444-4444-444444444444", "name": "staging"},
			{"id": "55555555-5555-5555-5555-555555555555", "name": "prod"},
		},
	})
	args = map[string]any{"project": "internal", "app": "bot", "key": "K"}
	msg := resolveAddressArgs(context.Background(), setEnvVarTool(), args, many.URL, "/api/v1")
	if msg == "" {
		t.Fatal("an ambiguous environment was guessed instead of refused — this is how a write lands in prod instead of staging")
	}
	if !strings.Contains(msg, "prod") || !strings.Contains(msg, "staging") {
		t.Errorf("refusal %q does not name the candidates, so the caller has to go listing", msg)
	}
	if _, filled := args["envId"]; filled {
		t.Error("envId was filled despite the refusal")
	}
}

func TestApplyAddressing_MakesIdsOptionalAndAdvertisesNames(t *testing.T) {
	g := setEnvVarTool()
	applyAddressing(&g)

	props := g.InputSchema["properties"].(map[string]any)
	for _, want := range []string{"ref", "project", "env", "app"} {
		if _, ok := props[want]; !ok {
			t.Errorf("input schema does not advertise %q, so a client will not offer it", want)
		}
	}
	for _, r := range g.InputSchema["required"].([]string) {
		if addressableParams[r] {
			t.Errorf("%q is still required, which forces the id walk this exists to remove", r)
		}
	}
	if !strings.Contains(g.Description, "ADDRESSING") {
		t.Error("tool description does not mention name addressing, so a model has to discover it by trial")
	}
}

func TestApplyToolDefaults_ListAppsIsThinUnlessAskedOtherwise(t *testing.T) {
	args := map[string]any{}
	applyToolDefaults("listApps", args)
	if args["view"] != "summary" {
		t.Errorf("listApps view = %v, want summary — the full snapshot is what overflowed a context window", args["view"])
	}

	args = map[string]any{"view": "full"}
	applyToolDefaults("listApps", args)
	if args["view"] != "full" {
		t.Error("an explicit view was overwritten by the default")
	}
}
