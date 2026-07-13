package mcp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

// expOnlyVerifier is a deliberately lenient sdkauth.TokenVerifier. It extracts
// only the token expiry so the MCP transport can emit an RFC 9728 401 challenge
// (WWW-Authenticate: Bearer resource_metadata=...) when a bearer is absent or
// expired — which is what makes a spec-compliant MCP client (e.g. Claude
// Desktop) start its OAuth flow and refresh on expiry.
//
// It does NOT verify the signature or issuer. Those, plus all RBAC, are enforced
// authoritatively by the backend on every self-proxied /api/v1 tool call, so the
// MCP layer is only an auth-discovery/refresh aid, never the security boundary.
// This keeps a valid console bearer from ever being wrongly rejected here.
func expOnlyVerifier(_ context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
	var claims jwt.RegisteredClaims
	if _, _, err := jwt.NewParser().ParseUnverified(token, &claims); err != nil {
		return nil, fmt.Errorf("%w: %v", sdkauth.ErrInvalidToken, err)
	}
	if claims.ExpiresAt == nil {
		return nil, fmt.Errorf("%w: token has no exp", sdkauth.ErrInvalidToken)
	}
	return &sdkauth.TokenInfo{
		Expiration: claims.ExpiresAt.Time,
		UserID:     claims.Subject,
	}, nil
}
