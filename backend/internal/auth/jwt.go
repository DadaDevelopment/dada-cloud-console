package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims represents the JWT claims payload.
type Claims struct {
	UserID      uuid.UUID `json:"user_id"`
	Username    string    `json:"username"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	// Fat IAM claims (ADR-009). dada-cloud authorizes purely from these: OrgRole
	// is the caller's role in OrgID; Projects maps project_id → role for
	// project-level grants; Scopes gates write surfaces via RequireScope.
	// Minted by user-service in keycloak mode, dev-god in local mode.
	OrgID    string            `json:"org_id,omitempty"`
	OrgRole  string            `json:"org_role,omitempty"`
	Projects map[string]string `json:"projects,omitempty"`
	Scopes   []string          `json:"scopes,omitempty"`

	// Groups carries Keycloak full-path groups; the only one still consumed for
	// authz is "/platform-admins" (internal staff god-mode, outside the enum).
	Groups []string `json:"groups,omitempty"`
	// Roles carries Keycloak realm/resource roles (MCP server / diagnostics);
	// no longer used for project authorization.
	Roles []string `json:"roles,omitempty"`
	jwt.RegisteredClaims
}

// AllScopes is the full scope vocabulary (PRD-IAM). Local dev-god tokens carry
// the whole set; user-service mints a narrower set for real keys.
var AllScopes = []string{
	"read", "metrics:write", "logs:write", "deploy:write",
	"builds:read", "builds:write", "admin",
}

// GenerateToken creates a signed JWT for local (HS256) auth mode. There is no
// Keycloak/user-service to mint fat claims locally, so the token grants dev-god
// access: Owner of a dev org with every scope. Keeps local dev/tests working
// while dada-cloud carries zero role-resolution logic (ADR-009).
func GenerateToken(userID uuid.UUID, username, email, displayName, secret string) (string, error) {
	claims := Claims{
		UserID:      userID,
		Username:    username,
		Email:       email,
		DisplayName: displayName,
		OrgID:       "local-dev",
		OrgRole:     "Owner",
		Scopes:      AllScopes,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   userID.String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("signing token: %w", err)
	}

	return signed, nil
}

// ValidateToken parses and validates a JWT token string, returning its claims.
func ValidateToken(tokenStr, secret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("parsing token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	return claims, nil
}
