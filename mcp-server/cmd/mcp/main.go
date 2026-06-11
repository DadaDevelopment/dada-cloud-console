// Command mcp boots the reflective MCP server: it loads the backend OpenAPI
// spec, reflects one MCP tool per operation, applies overrides.yaml, and serves
// the tools over Streamable HTTP, proxying calls to the backend REST API.
//
// M2: bearer passthrough only (no validation). The inbound Authorization header
// is forwarded verbatim to the backend. M3 will add Keycloak/JWKS validation.
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/dada-tuda/console/mcp-server/internal/auth"
	"github.com/dada-tuda/console/mcp-server/internal/overrides"
	"github.com/dada-tuda/console/mcp-server/internal/reflect"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverName    = "dada-cloud-mcp"
	serverVersion = "0.2.0" // M2
)

func main() {
	cfg := loadConfig()
	ctx := context.Background()

	spec, err := reflect.LoadSpec(ctx, cfg.openAPIPath, cfg.backendURL)
	if err != nil {
		log.Fatalf("load spec: %v", err)
	}

	tools := reflect.GenerateTools(spec)

	ov, err := overrides.Load(cfg.overridesPath)
	if err != nil {
		log.Fatalf("load overrides: %v", err)
	}
	tools = overrides.Apply(tools, ov)

	srv := buildServer(tools, cfg.backendURL, spec.BasePath)

	// Boot logging: tool count + operationId fallbacks.
	fallbacks := 0
	for _, t := range tools {
		if t.FallbackName {
			fallbacks++
			log.Printf("tool %q uses a synthesised name (operationId missing)", t.Name)
		}
	}
	log.Printf("registered %d tools (basePath=%s, backend=%s); %d operationId fallbacks",
		len(tools), spec.BasePath, cfg.backendURL, fallbacks)

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// Bearer middleware stashes Authorization in request ctx (passthrough).
	mux.Handle("/", auth.Middleware(handler))

	addr := ":" + cfg.port
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

// buildServer creates the MCP server and registers every tool. Each tool's
// proxy handler reads the bearer from ctx; because the SDK's streamable HTTP
// transport does not propagate the per-request http context into handler ctx,
// we wrap each handler to lift the per-request Authorization header (exposed via
// req.GetExtra().Header) into ctx before delegating to the proxy.
func buildServer(tools []reflect.GeneratedTool, backendURL, basePath string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: serverVersion}, nil)

	for _, g := range tools {
		proxy := reflect.MakeHandler(g, backendURL, basePath)
		tool := newTool(g)
		srv.AddTool(tool, wrapBearer(proxy))
	}
	return srv
}

// wrapBearer lifts the per-request Authorization header into ctx so the proxy
// (which reads bearer from ctx) works under streamable HTTP. Falls back to any
// bearer already in ctx (unit tests inject it directly).
func wrapBearer(h reflect.ToolHandler) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if auth.BearerFromContext(ctx) == "" {
			if extra := req.GetExtra(); extra != nil && extra.Header != nil {
				if b := extra.Header.Get("Authorization"); b != "" {
					ctx = auth.WithBearer(ctx, b)
				}
			}
		}
		return h(ctx, req)
	}
}

func newTool(g reflect.GeneratedTool) *mcp.Tool {
	readOnly := g.ReadOnly
	destructive := g.Destructive
	return &mcp.Tool{
		Name:        g.Name,
		Description: g.Description,
		InputSchema: g.InputSchema,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    readOnly,
			DestructiveHint: &destructive,
		},
	}
}

type config struct {
	port          string
	backendURL    string
	openAPIPath   string
	overridesPath string
}

func loadConfig() config {
	return config{
		port:          envOr("MCP_PORT", "8090"),
		backendURL:    os.Getenv("BACKEND_URL"),
		openAPIPath:   os.Getenv("MCP_OPENAPI_PATH"),
		overridesPath: envOr("MCP_OVERRIDES_PATH", "overrides.yaml"),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
