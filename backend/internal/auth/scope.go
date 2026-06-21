package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HasScope reports whether the claims carry the given scope. Scopes come from
// fat claims minted by the IAM gateway when it exchanges an API key.
func HasScope(claims *Claims, want string) bool {
	if claims == nil {
		return false
	}
	for _, s := range claims.Scopes {
		if s == want {
			return true
		}
	}
	return false
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
