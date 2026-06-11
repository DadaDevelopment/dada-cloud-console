package reflect

import (
	"context"
	"testing"
)

func loadFixture(t *testing.T, path string) []GeneratedTool {
	t.Helper()
	spec, err := LoadSpec(context.Background(), path, "")
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if spec.BasePath != "/api/v1" {
		t.Fatalf("basePath = %q, want /api/v1", spec.BasePath)
	}
	return GenerateTools(spec)
}

func byName(tools []GeneratedTool, name string) (GeneratedTool, bool) {
	for _, t := range tools {
		if t.Name == name {
			return t, true
		}
	}
	return GeneratedTool{}, false
}

func TestGenerateTools_Fixture(t *testing.T) {
	tools := loadFixture(t, "testdata/swagger.json")

	if len(tools) != 3 {
		t.Fatalf("tool count = %d, want 3", len(tools))
	}

	// GET with path + query params.
	get, ok := byName(tools, "listOperations")
	if !ok {
		t.Fatal("listOperations missing")
	}
	if !get.ReadOnly || get.Destructive {
		t.Errorf("listOperations flags: readonly=%v destructive=%v", get.ReadOnly, get.Destructive)
	}
	if !contains(get.PathParams, "projectId") {
		t.Errorf("listOperations path params = %v", get.PathParams)
	}
	if !contains(get.QueryParams, "limit") {
		t.Errorf("listOperations query params = %v", get.QueryParams)
	}
	req := requiredOf(t, get)
	if !contains(req, "projectId") {
		t.Errorf("projectId should be required, got %v", req)
	}
	if contains(req, "limit") {
		t.Errorf("limit should be optional, got required %v", req)
	}

	// POST with typed body — flattened props + required.
	post, ok := byName(tools, "createDatabase")
	if !ok {
		t.Fatal("createDatabase missing")
	}
	if post.ReadOnly || post.Destructive {
		t.Errorf("createDatabase flags: readonly=%v destructive=%v", post.ReadOnly, post.Destructive)
	}
	props := propsOf(t, post)
	for _, want := range []string{"appName", "plan", "replicas", "projectId", "envId"} {
		if _, has := props[want]; !has {
			t.Errorf("createDatabase missing flattened prop %q (have %v)", want, keys(props))
		}
	}
	if !contains(post.BodyProps, "appName") || !contains(post.BodyProps, "plan") {
		t.Errorf("createDatabase BodyProps = %v", post.BodyProps)
	}
	preq := requiredOf(t, post)
	for _, want := range []string{"projectId", "envId", "appName", "plan"} {
		if !contains(preq, want) {
			t.Errorf("createDatabase required missing %q (have %v)", want, preq)
		}
	}
	if contains(preq, "replicas") {
		t.Errorf("replicas should be optional, got required %v", preq)
	}
	// enum carried through.
	if plan, ok := props["plan"].(map[string]any); ok {
		if _, has := plan["enum"]; !has {
			t.Errorf("plan should carry enum, got %v", plan)
		}
	} else {
		t.Errorf("plan prop not a map: %v", props["plan"])
	}

	// DELETE — destructive.
	del, ok := byName(tools, "deleteModel")
	if !ok {
		t.Fatal("deleteModel missing")
	}
	if del.ReadOnly || !del.Destructive {
		t.Errorf("deleteModel flags: readonly=%v destructive=%v", del.ReadOnly, del.Destructive)
	}
}

// TestGenerateTools_RealBackendSpec is the dev-proof against the real spec.
func TestGenerateTools_RealBackendSpec(t *testing.T) {
	tools := loadFixture(t, "testdata/backend-swagger.json")

	if len(tools) < 30 {
		t.Fatalf("real spec generated %d tools, want >= 30", len(tools))
	}

	for _, want := range []string{"createDatabase", "promoteModel", "listProjects", "getOperation"} {
		if _, ok := byName(tools, want); !ok {
			t.Errorf("expected tool %q not generated", want)
		}
	}

	// Sanity: a known GET is read-only, a known DELETE is destructive.
	if g, ok := byName(tools, "listProjects"); ok && (!g.ReadOnly || g.Destructive) {
		t.Errorf("listProjects flags wrong: %+v", g)
	}
	if g, ok := byName(tools, "deleteModel"); ok && !g.Destructive {
		t.Errorf("deleteModel should be destructive")
	}
}

// helpers

func propsOf(t *testing.T, g GeneratedTool) map[string]any {
	t.Helper()
	p, ok := g.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s: no properties map", g.Name)
	}
	return p
}

func requiredOf(t *testing.T, g GeneratedTool) []string {
	t.Helper()
	r, ok := g.InputSchema["required"].([]string)
	if !ok {
		return nil
	}
	return r
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
