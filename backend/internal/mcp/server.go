package mcp

import (
	"context"
	"log"
	"net/http"
	"strings"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const (
	serverName    = "dada-cloud-mcp"
	serverVersion = "0.3.0"
)

// mcpScopesSupported is advertised in the RFC 9728 protected-resource metadata.
// A spec-compliant MCP client requests exactly this set, so it MUST be a subset
// of what the dada-mcp Keycloak client can grant (see argo-infra
// iam-client-scopes.yaml). Without it, Claude Desktop falls back to the AS's
// full scopes_supported list (phone, address, service_account, ...) which the
// public client cannot grant -> Keycloak rejects with invalid_scope before the
// login page renders. Mirrors the console SPA's write set plus refresh + reads.
var mcpScopesSupported = []string{
	"openid", "profile", "email", "offline_access",
	"read", "builds:read", "builds:write", "deploy:write",
}

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
	// RequireBearer, when true, wraps the MCP transport so a missing/expired
	// bearer yields a 401 + WWW-Authenticate (RFC 9728) instead of a buried
	// tool-level error, letting OAuth clients discover the auth server and start
	// their flow. Set from AUTH_MODE=keycloak; local dev leaves it false.
	RequireBearer bool
}

// NewHandler builds the MCP http.Handler from the embedded Swagger spec.
// Mount at /mcp in the backend's HTTP mux. The handler includes:
//   - /.well-known/oauth-protected-resource (RFC 9728)
//   - /  (Streamable HTTP MCP transport)
//
// Each tool call self-proxies to backendURL/api/v1/... so all auth middleware
// and handler logic runs unchanged. The inbound Authorization header is read
// from each MCP request and forwarded to the self-proxy call.
//
// The transport runs stateless: the backend serves several replicas behind an
// ingress with no session affinity, so a client's initialize and its follow-up
// tools/list land on different pods and an in-memory session would answer
// "session not found" (404) about half the time. Nothing here needs a session —
// every tool is a stateless self-proxy call authorised by the request's own
// bearer, and the server issues no server->client requests. The cost is that
// GET (server-initiated SSE stream) now answers 405; no client uses it.
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

	mcpHandler := sdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *sdkmcp.Server { return srv },
		&sdkmcp.StreamableHTTPOptions{Stateless: true},
	)

	var transport http.Handler = bearerMiddleware(mcpHandler)
	if cfg.RequireBearer {
		transport = sdkauth.RequireBearerToken(expOnlyVerifier, &sdkauth.RequireBearerTokenOptions{
			ResourceMetadataURL: strings.TrimRight(cfg.ResourceURL, "/") + "/.well-known/oauth-protected-resource",
		})(transport)
	}

	mux := http.NewServeMux()
	mux.Handle("/.well-known/oauth-protected-resource", MetadataHandler(cfg))
	mux.Handle("/", transport)

	return mux, nil
}

// MetadataHandler returns just the RFC 9728 protected-resource metadata handler.
// The main handler mounts it under /mcp, but spec-compliant OAuth clients look it
// up at the HOST ROOT — /.well-known/oauth-protected-resource and the resource-path
// suffixed /.well-known/oauth-protected-resource/mcp (RFC 9728 section 3.1). Export
// it so the backend router can serve those host-root paths too; otherwise strict
// clients (e.g. Claude connectors) 404 on discovery and never reach the auth flow.
func MetadataHandler(cfg Config) http.Handler {
	return sdkauth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:               cfg.ResourceURL,
		AuthorizationServers:   []string{cfg.KeycloakIssuer},
		BearerMethodsSupported: []string{"header"},
		ScopesSupported:        mcpScopesSupported,
	})
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
