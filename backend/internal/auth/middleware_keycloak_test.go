package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func init() { gin.SetMode(gin.TestMode) }

// TestKeycloakMiddleware_PopulatesClaims drives a real verifier (mock JWKS) plus
// a stub resolver through the middleware and asserts the downstream handler sees
// the local Claims via GetClaims — the contract all ~50 handlers depend on.
func TestKeycloakMiddleware_PopulatesClaims(t *testing.T) {
	mk := newMockKeycloak(t)
	v := newVerifier(t, mk, false)

	localID := uuid.New()
	resolver := func(c *gin.Context, kc *KeycloakClaims) (*Claims, error) {
		return &Claims{
			UserID:      localID,
			Username:    kc.PreferredUsername,
			Email:       kc.Email,
			DisplayName: kc.Name,
			Groups:      kc.Groups,
			Roles:       kc.Roles,
		}, nil
	}

	r := gin.New()
	var seen *Claims
	r.GET("/x", KeycloakMiddleware(v, resolver), func(c *gin.Context) {
		cl, ok := GetClaims(c)
		if !ok {
			c.Status(http.StatusUnauthorized)
			return
		}
		seen = cl
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+mk.mint(t, "key-1", nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if seen == nil || seen.UserID != localID {
		t.Fatalf("claims not populated: %+v", seen)
	}
	if seen.Username != "alice" || seen.DisplayName != "Alice Example" {
		t.Errorf("identity wrong: %+v", seen)
	}
	if len(seen.Groups) != 2 || len(seen.Roles) != 3 {
		t.Errorf("groups/roles not threaded: groups=%v roles=%v", seen.Groups, seen.Roles)
	}
}

func TestKeycloakMiddleware_MissingHeader(t *testing.T) {
	mk := newMockKeycloak(t)
	v := newVerifier(t, mk, false)
	resolver := func(c *gin.Context, kc *KeycloakClaims) (*Claims, error) { return &Claims{}, nil }

	r := gin.New()
	r.GET("/x", KeycloakMiddleware(v, resolver), func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestKeycloakMiddleware_SignupClosedReturns403(t *testing.T) {
	mk := newMockKeycloak(t)
	v := newVerifier(t, mk, false)
	resolver := func(c *gin.Context, kc *KeycloakClaims) (*Claims, error) {
		return nil, ErrSignupClosed
	}

	r := gin.New()
	r.GET("/x", KeycloakMiddleware(v, resolver), func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+mk.mint(t, "key-1", nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"code":"signup_closed"`) {
		t.Errorf("body = %s, want code=signup_closed", w.Body.String())
	}
}

func TestKeycloakMiddleware_BadTokenRejected(t *testing.T) {
	mk := newMockKeycloak(t)
	v := newVerifier(t, mk, false)
	resolver := func(c *gin.Context, kc *KeycloakClaims) (*Claims, error) { return &Claims{}, nil }

	r := gin.New()
	r.GET("/x", KeycloakMiddleware(v, resolver), func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}
