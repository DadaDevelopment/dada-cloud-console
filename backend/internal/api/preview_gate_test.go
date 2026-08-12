package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
)

func previewTestCfg() *config.Config {
	return &config.Config{
		PreviewHostBase:   "pv.dada-tuda.ru",
		PreviewHostSecret: "test-secret",
		PublicBaseURL:     "https://console.dada-tuda.ru",
	}
}

func TestPreviewHostRoundTrip(t *testing.T) {
	cfg := previewTestCfg()
	envID := uuid.MustParse("7722dcff-7a44-4cac-a6da-eb362088c239")

	host := PreviewHostFor("profi", envID, cfg)
	if host == "" {
		t.Fatal("PreviewHostFor returned empty host")
	}
	if !strings.HasSuffix(host, ".pv.dada-tuda.ru") {
		t.Fatalf("host %q not under preview base", host)
	}

	label := strings.TrimSuffix(host, ".pv.dada-tuda.ru")
	app, env8, mac12, ok := parsePreviewLabel(label)
	if !ok {
		t.Fatalf("parsePreviewLabel(%q) failed", label)
	}
	if app != "profi" {
		t.Errorf("app = %q, want profi", app)
	}
	if env8 != "7722dcff" {
		t.Errorf("env8 = %q, want 7722dcff", env8)
	}
	if mac12 != previewMAC("profi", envID, cfg.PreviewHostSecret) {
		t.Errorf("mac mismatch: %q", mac12)
	}
}

func TestPreviewHostRoundTrip_DashedAppName(t *testing.T) {
	cfg := previewTestCfg()
	envID := uuid.New()

	host := PreviewHostFor("fonbet-value", envID, cfg)
	label := strings.TrimSuffix(host, ".pv.dada-tuda.ru")
	app, env8, _, ok := parsePreviewLabel(label)
	if !ok {
		t.Fatalf("parsePreviewLabel(%q) failed", label)
	}
	if app != "fonbet-value" {
		t.Errorf("app = %q, want fonbet-value", app)
	}
	if env8 != envID.String()[:8] {
		t.Errorf("env8 = %q, want %q", env8, envID.String()[:8])
	}
}

func TestPreviewHostFor_DisabledWithoutSecret(t *testing.T) {
	cfg := &config.Config{PreviewHostBase: "pv.dada-tuda.ru"}
	if h := PreviewHostFor("profi", uuid.New(), cfg); h != "" {
		t.Errorf("expected empty host without secret, got %q", h)
	}
}

func TestPreviewHostFor_LabelTooLong(t *testing.T) {
	cfg := previewTestCfg()
	longApp := strings.Repeat("a", 60)
	if h := PreviewHostFor(longApp, uuid.New(), cfg); h != "" {
		t.Errorf("expected empty host for oversized label, got %q", h)
	}
}

func TestWritePreviewUpstreamUnavailable(t *testing.T) {
	rec := httptest.NewRecorder()

	writePreviewUpstreamUnavailable(rec)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	if got := rec.Header().Get(previewErrorCodeHeader); got != previewUpstreamUnavailableCode {
		t.Errorf("%s = %q, want %q", previewErrorCodeHeader, got, previewUpstreamUnavailableCode)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "платформы") {
		t.Errorf("body must name itself as a platform response, got %q", body)
	}
	if !strings.Contains(body, "не вашего приложения") {
		t.Errorf("body must disclaim the user's app as the cause, got %q", body)
	}
}

func TestScrubFrameHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; img-src *")
	h.Add("Content-Security-Policy", "frame-ancestors 'self'")

	scrubFrameHeaders(h, "https://console.dada-tuda.ru")

	if got := h.Get("X-Frame-Options"); got != "" {
		t.Errorf("X-Frame-Options survived: %q", got)
	}
	values := h.Values("Content-Security-Policy")
	for _, v := range values {
		if strings.Contains(v, "'none'") {
			t.Errorf("app frame-ancestors survived: %q", v)
		}
	}
	joined := strings.Join(values, " || ")
	if !strings.Contains(joined, "default-src 'self'") || !strings.Contains(joined, "img-src *") {
		t.Errorf("non-frame CSP directives lost: %q", joined)
	}
	if !strings.Contains(joined, "frame-ancestors 'self' https://console.dada-tuda.ru") {
		t.Errorf("console frame-ancestors policy missing: %q", joined)
	}
	if got := h.Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
		t.Errorf("X-Robots-Tag = %q, want noindex", got)
	}
}

func TestScrubFrameHeaders_NoCSP(t *testing.T) {
	h := http.Header{}
	scrubFrameHeaders(h, "https://console.dada-tuda.ru")
	if got := h.Get("Content-Security-Policy"); got != "frame-ancestors 'self' https://console.dada-tuda.ru" {
		t.Errorf("CSP = %q", got)
	}
}

func TestDropFrameAncestors_OnlyDirective(t *testing.T) {
	if got := dropFrameAncestors("frame-ancestors 'none'"); got != "" {
		t.Errorf("dropFrameAncestors = %q, want empty", got)
	}
}

func TestRewriteLocation(t *testing.T) {
	cases := []struct {
		name, location, appHost, want string
	}{
		{"same app absolute", "https://profi.dada-tuda.ru/login", "profi.dada-tuda.ru", "https://profi-7722dcff-abc.pv.dada-tuda.ru/login"},
		{"relative untouched", "/login", "profi.dada-tuda.ru", "/login"},
		{"foreign host untouched", "https://accounts.google.com/auth", "profi.dada-tuda.ru", "https://accounts.google.com/auth"},
		{"no app host known", "https://profi.dada-tuda.ru/login", "", "https://profi.dada-tuda.ru/login"},
	}
	for _, tc := range cases {
		h := http.Header{}
		h.Set("Location", tc.location)
		rewriteLocation(h, tc.appHost, "profi-7722dcff-abc.pv.dada-tuda.ru")
		if got := h.Get("Location"); got != tc.want {
			t.Errorf("%s: Location = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestParsePreviewLabel_Rejects(t *testing.T) {
	for _, label := range []string{"", "profi", "a-b", "profi-7722dcff-short", "profi-7722dc-123456789012", "-7722dcff-123456789012"} {
		if _, _, _, ok := parsePreviewLabel(label); ok {
			t.Errorf("parsePreviewLabel(%q) accepted, want reject", label)
		}
	}
}
