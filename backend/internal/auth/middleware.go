package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const claimsKey = "claims"

// bearerToken extracts the raw token from an "Authorization: Bearer <token>"
// header, or "" if the header is missing/malformed.
func bearerToken(c *gin.Context) string {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return parts[1]
}

// GinMiddleware returns a Gin handler that validates the Authorization Bearer JWT.
func GinMiddleware(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing Authorization header"})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid Authorization header format"})
			return
		}

		claims, err := ValidateToken(parts[1], jwtSecret)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set(claimsKey, claims)
		c.Next()
	}
}

// KeycloakMiddleware returns a Gin handler that validates a Keycloak RS256
// access token, resolves it to a local users.id, and stores a *Claims under the
// same context key GetClaims reads — so all ~50 existing handlers keep working
// unchanged. verifier and resolver are injected to keep this testable; resolver
// maps verified Keycloak claims to a local user id (see ResolveUser).
func KeycloakMiddleware(
	verifier *KeycloakVerifier,
	resolver func(c *gin.Context, kc *KeycloakClaims) (*Claims, error),
) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := bearerToken(c)
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or malformed Authorization header"})
			return
		}

		kc, err := verifier.Verify(c.Request.Context(), raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		claims, err := resolver(c, kc)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "could not resolve identity"})
			return
		}

		c.Set(claimsKey, claims)
		c.Next()
	}
}

// SetClaims stores resolved claims in the Gin context under the same key
// GetClaims reads. Used by alternative authenticators (e.g. the monitoring
// scoped-key ingest middleware) that resolve identity outside the JWT path.
func SetClaims(c *gin.Context, claims *Claims) {
	c.Set(claimsKey, claims)
}

// GetClaims retrieves JWT claims stored in the Gin context.
func GetClaims(c *gin.Context) (*Claims, bool) {
	val, exists := c.Get(claimsKey)
	if !exists {
		return nil, false
	}
	claims, ok := val.(*Claims)
	return claims, ok
}
