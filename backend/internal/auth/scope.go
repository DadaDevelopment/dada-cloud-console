package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HasScope reports whether the claims carry the given scope. Scopes are decoded
// from the native space-delimited OIDC `scope` claim (ADR-009).
func HasScope(claims *Claims, want string) bool {
	if claims == nil {
		return false
	}
	return claims.HasScope(want)
}

// RequireScope returns a Gin middleware that aborts with 403 unless the request
// claims carry the given scope. It runs after the auth middleware (which sets
// the claims), so a missing claims object means 401.
func RequireScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := GetClaims(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		if !HasScope(claims, scope) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "missing required scope: " + scope})
			return
		}
		c.Next()
	}
}
