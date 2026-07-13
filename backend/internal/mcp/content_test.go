package mcp

import (
	"context"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectTestClient wires an in-memory client to a server carrying just the
// prompts and resources (no tools needed to exercise content).
func connectTestClient(t *testing.T) *sdkmcp.ClientSession {
	t.Helper()
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: serverName, Version: serverVersion}, nil)
	registerContent(srv, []byte(`{"swagger":"2.0"}`), []GeneratedTool{
		{Name: "createApp", Description: "Deploy a new app\n\nProvisions..."},
		{Name: "setEnvVar", Description: "Set an environment variable"},
	})

	ctx := context.Background()
	st, ct := sdkmcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cli := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := cli.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestPromptsRegistered(t *testing.T) {
	cs := connectTestClient(t)
	ctx := context.Background()

	list, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	want := map[string]bool{"deploy-app": false, "configure-env": false, "diagnose-app": false}
	for _, p := range list.Prompts {
		if _, ok := want[p.Name]; ok {
			want[p.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("prompt %q not listed", name)
		}
	}

	got, err := cs.GetPrompt(ctx, &sdkmcp.GetPromptParams{
		Name:      "deploy-app",
		Arguments: map[string]string{"project": "acme", "name": "web", "image": "reg/web:1", "port": "3000"},
	})
	if err != nil {
		t.Fatalf("GetPrompt deploy-app: %v", err)
	}
	if len(got.Messages) == 0 {
		t.Fatal("deploy-app returned no messages")
	}
	tc, ok := got.Messages[0].Content.(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", got.Messages[0].Content)
	}
	for _, sub := range []string{"acme", "web", "createApp", "listApps", "reg/web:1", "3000"} {
		if !strings.Contains(tc.Text, sub) {
			t.Errorf("deploy-app body missing %q", sub)
		}
	}
}

func TestResourcesRegistered(t *testing.T) {
	cs := connectTestClient(t)
	ctx := context.Background()

	list, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	want := map[string]bool{
		"dada://guide/getting-started":  false,
		"dada://reference/tools":        false,
		"dada://reference/openapi.json": false,
	}
	for _, r := range list.Resources {
		if _, ok := want[r.URI]; ok {
			want[r.URI] = true
		}
	}
	for uri, seen := range want {
		if !seen {
			t.Errorf("resource %q not listed", uri)
		}
	}

	rr, err := cs.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: "dada://reference/openapi.json"})
	if err != nil {
		t.Fatalf("ReadResource openapi: %v", err)
	}
	if len(rr.Contents) == 0 || !strings.Contains(rr.Contents[0].Text, `"swagger":"2.0"`) {
		t.Errorf("openapi resource did not return spec bytes: %+v", rr.Contents)
	}

	ti, err := cs.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: "dada://reference/tools"})
	if err != nil {
		t.Fatalf("ReadResource tools: %v", err)
	}
	if !strings.Contains(ti.Contents[0].Text, "createApp") || !strings.Contains(ti.Contents[0].Text, "setEnvVar") {
		t.Errorf("tool index missing tools: %s", ti.Contents[0].Text)
	}
}
