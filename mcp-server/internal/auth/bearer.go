// Package auth handles bearer passthrough for M2.
//
// M2 does NOT validate tokens (Keycloak/JWKS arrives in M3). It simply reads
// the inbound Authorization header and forwards it verbatim to the backend,
// which still owns auth. The middleware stashes the raw bearer in the request
// context; the proxy handler reads it from ctx so the proxy stays decoupled
// from the transport (and testable by injecting ctx directly).
package auth

import (
	"context"
	"net/http"
)

type ctxKey struct{}

var bearerKey ctxKey

// WithBearer returns ctx carrying the raw Authorization header value
// (e.g. "Bearer eyJ...").
func WithBearer(ctx context.Context, bearer string) context.Context {
	return context.WithValue(ctx, bearerKey, bearer)
}

// BearerFromContext returns the raw Authorization value stashed by the
// middleware, or "" if none.
func BearerFromContext(ctx context.Context) string {
	v, _ := ctx.Value(bearerKey).(string)
	return v
}

// Middleware reads the Authorization header and stashes it in the request
// context for downstream handlers. Absent header => no bearer (backend will
// 401, surfaced to the agent as a tool error).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b := r.Header.Get("Authorization"); b != "" {
			r = r.WithContext(WithBearer(r.Context(), b))
		}
		next.ServeHTTP(w, r)
	})
}
