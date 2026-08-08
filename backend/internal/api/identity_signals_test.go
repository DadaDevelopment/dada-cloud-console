package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestUAFamilyBucketsAutomationApartFromBrowsers(t *testing.T) {
	cases := map[string]string{
		"": "none",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/147.0.7727.15 Safari/537.36": "headless-chrome",
		"curl/8.4.0":                          "cli",
		"python-requests/2.31.0":              "cli",
		"Go-http-client/2.0":                  "cli",
		"Mozilla/5.0 Firefox/128.0":           "firefox",
		"Mozilla/5.0 Chrome/126.0.0.0 Safari": "chrome",
		"Mozilla/5.0 Version/17.0 Safari/605": "safari",
		"Mozilla/5.0 Chrome/126 Edg/126.0":    "edge",
		"SomeBespokeAgent/1.0":                "other",
	}
	for ua, want := range cases {
		if got := uaFamily(ua); got != want {
			t.Errorf("uaFamily(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestCollectIdentitySignalReadsHeadersAndClips(t *testing.T) {
	gin.SetMode(gin.TestMode)
	long := strings.Repeat("x", identitySignalFieldMax+50)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("User-Agent", long)
	c.Request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	c.Request.Header.Set("Sec-Ch-Ua-Platform", "\"Linux\"")
	c.Request.Header.Set("Sec-Ch-Ua-Mobile", "?0")

	id := uuid.New()
	sig := collectIdentitySignal(c, id, "signup")

	if sig.UserID != id || sig.Event != "signup" {
		t.Fatalf("identity mismatch: %+v", sig)
	}
	if len(sig.UserAgent) != identitySignalFieldMax {
		t.Errorf("user agent not clipped: len = %d, want %d", len(sig.UserAgent), identitySignalFieldMax)
	}
	if sig.AcceptLanguage != "zh-CN,zh;q=0.9" {
		t.Errorf("accept-language = %q", sig.AcceptLanguage)
	}
	if sig.ClientHints["Sec-Ch-Ua-Platform"] != "\"Linux\"" {
		t.Errorf("client hints missing platform: %+v", sig.ClientHints)
	}
	if _, ok := sig.ClientHints["Sec-Ch-Ua-Arch"]; ok {
		t.Errorf("absent hint must not be stored as empty: %+v", sig.ClientHints)
	}
}

// TestClientIPIgnoresForgedForwardedFor is the whole point of the trusted-proxy
// list: an untrusted caller must not be able to name its own address.
func TestClientIPIgnoresForgedForwardedFor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := r.SetTrustedProxies(trustedProxyCIDRs); err != nil {
		t.Fatalf("set trusted proxies: %v", err)
	}
	var seen string
	r.GET("/probe", func(c *gin.Context) {
		seen = c.ClientIP()
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.RemoteAddr = "203.0.113.7:44321"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.ServeHTTP(httptest.NewRecorder(), req)

	if seen == "1.2.3.4" {
		t.Fatalf("forged X-Forwarded-For was trusted: ClientIP = %q", seen)
	}
	if seen != "203.0.113.7" {
		t.Fatalf("ClientIP = %q, want the real peer 203.0.113.7", seen)
	}
}

func TestClientIPTrustsInClusterHop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := r.SetTrustedProxies(trustedProxyCIDRs); err != nil {
		t.Fatalf("set trusted proxies: %v", err)
	}
	var seen string
	r.GET("/probe", func(c *gin.Context) {
		seen = c.ClientIP()
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.RemoteAddr = "10.244.3.49:52110"
	req.Header.Set("X-Forwarded-For", "198.51.100.22")
	r.ServeHTTP(httptest.NewRecorder(), req)

	if seen != "198.51.100.22" {
		t.Fatalf("ClientIP = %q, want 198.51.100.22 forwarded by the ingress hop", seen)
	}
}
