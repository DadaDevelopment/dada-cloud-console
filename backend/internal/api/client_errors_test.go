package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func postClientError(body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	r := gin.New()
	r.POST("/api/v1/client-errors", h.ReportClientError)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/client-errors", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

func TestReportClientError(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"valid", `{"message":"boom","stack":"at x","url":"https://console/x","kind":"react"}`},
		{"empty message", `{"message":"","stack":"x"}`},
		{"malformed json", `{not json`},
		{"missing kind defaults", `{"message":"hooks.map is not a function"}`},
		{"oversized message capped", `{"message":"` + strings.Repeat("A", 5000) + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postClientError(tc.body)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("code=%d want 204 (body=%s)", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestClampLen(t *testing.T) {
	if got := clampLen("abcdef", 3); got != "abc" {
		t.Fatalf("clampLen=%q want abc", got)
	}
	if got := clampLen("ab", 10); got != "ab" {
		t.Fatalf("clampLen=%q want ab", got)
	}
}
