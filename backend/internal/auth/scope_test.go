package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHasScope(t *testing.T) {
	c := &Claims{Scopes: []string{"metrics:write", "logs:write"}}
	if !HasScope(c, "metrics:write") {
		t.Error("expected metrics:write present")
	}
	if HasScope(c, "metrics:read") {
		t.Error("did not expect metrics:read")
	}
	if HasScope(nil, "metrics:write") {
		t.Error("nil claims must not have scope")
	}
}

func TestRequireScope(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name     string
		claims   *Claims // nil = no claims set
		want     int
	}{
		{"no claims -> 401", nil, http.StatusUnauthorized},
		{"wrong scope -> 403", &Claims{Scopes: []string{"logs:write"}}, http.StatusForbidden},
		{"right scope -> 200", &Claims{Scopes: []string{"metrics:write"}}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			if tc.claims != nil {
				c.Set(claimsKey, tc.claims)
			}
			handled := false
			mw := RequireScope("metrics:write")
			mw(c)
			if !c.IsAborted() {
				handled = true
				c.Status(http.StatusOK)
			}
			if tc.want == http.StatusOK && !handled {
				t.Errorf("expected next handler to run")
			}
			if w.Code != tc.want {
				t.Errorf("status: got %d want %d", w.Code, tc.want)
			}
		})
	}
}
