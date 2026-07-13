package mcp

import (
	"context"
	"log"
	"net/http"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const (
	serverName    = "dada-cloud-mcp"
	serverVersion = "0.3.0"
)

// Config holds the runtime config for the embedded MCP server.
type Config struct {
	// BackendURL is used for the self-proxy loop (e.g. "http://127.0.0.1:8080").
	BackendURL string
	// OverridesPath is an optional path to overrides.yaml. Missing file = no-op.
	OverridesPath string
	// ResourceURL is the OAuth protected resource identifier (RFC 9728).
	ResourceURL string
	// KeycloakIssuer is the Keycloak issuer URL for the protected resource metadata.
	KeycloakIssuer string
}

// NewHandler builds the MCP http.Handler from the embedded Swagger spec.
// Mount at /mcp in the backend's HTTP mux. The handler includes:
//   - /.well-known/oauth-protected-resource (RFC 9728)
//   - /  (Streamable HTTP MCP transport)
//
// Each tool call self-proxies to backendURL/api/v1/... so all auth middleware
// and handler logic runs unchanged. The inbound Authorization header is read
// from each MCP request and forwarded to the self-proxy call.
func NewHandler(specBytes []byte, cfg Config) (http.Handler, error) {
	spec, err := ParseSpec(specBytes)
	if err != nil {
		return nil, err
	}

	tools := GenerateTools(spec)

	ov, err := LoadOverrides(cfg.OverridesPath)
	if err != nil {
		return nil, err
	}
	tools = ApplyOverrides(tools, ov)

	srv := buildMCPServer(tools, cfg.BackendURL, spec.BasePath)
	registerContent(srv, specBytes, tools)

	fallbacks := 0
	for _, t := range tools {
		if t.FallbackName {
			fallbacks++
			log.Printf("mcp: tool %q uses synthesised name (operationId missing)", t.Name)
		}
	}
	log.Printf("mcp: %d tools registered (basePath=%s, backend=%s); %d fallbacks",
		len(tools), spec.BasePath, cfg.BackendURL, fallbacks)

	resourceMeta := &oauthex.ProtectedResourceMetadata{
		Resource:               cfg.ResourceURL,
		AuthorizationServers:   []string{cfg.KeycloakIssuer},
		BearerMethodsSupported: []string{"header"},
	}

	mcpHandler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return srv }, nil)

	mux := http.NewServeMux()
	mux.Handle("/.well-known/oauth-protected-resource",
		sdkauth.ProtectedResourceMetadataHandler(resourceMeta))
	mux.Handle("/", bearerMiddleware(mcpHandler))

	return mux, nil
}

func buildMCPServer(tools []GeneratedTool, backendURL, basePath string) *sdkmcp.Server {
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: serverName, Version: serverVersion}, nil)
	for _, g := range tools {
		proxy := MakeHandler(g, backendURL, basePath)
		tool := &sdkmcp.Tool{
			Name:        g.Name,
			Description: g.Description,
			InputSchema: g.InputSchema,
			Annotations: &sdkmcp.ToolAnnotations{
				ReadOnlyHint:    g.ReadOnly,
				DestructiveHint: boolPtr(g.Destructive),
			},
		}
		destructive := g.Destructive
		readOnly := g.ReadOnly
		_ = destructive
		_ = readOnly
		srv.AddTool(tool, wrapBearer(proxy))
	}
	return srv
}

// bearerMiddleware stashes the inbound Authorization header in request context.
func bearerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b := r.Header.Get("Authorization"); b != "" {
			r = r.WithContext(WithBearer(r.Context(), b))
		}
		next.ServeHTTP(w, r)
	})
}

// wrapBearer lifts the per-request Authorization header into the tool handler
// ctx. The SDK's streamable HTTP transport does NOT propagate the per-POST
// http.Request context into tool-call handler ctx — it runs on the session's
// run loop. We read it from req.GetExtra().Header instead.
func wrapBearer(h ToolHandler) sdkmcp.ToolHandler {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		if BearerFromContext(ctx) == "" {
			if extra := req.GetExtra(); extra != nil && extra.Header != nil {
				if b := extra.Header.Get("Authorization"); b != "" {
					ctx = WithBearer(ctx, b)
				}
			}
		}
		return h(ctx, req)
	}
}

func boolPtr(b bool) *bool { return &b }
