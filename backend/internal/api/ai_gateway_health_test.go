package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// aiCounter reads one labelled sample out of the default registry, following the
// dto.Metric idiom the metrics package tests already use rather than adding
// prometheus/testutil as a module dependency. Absent series read as 0, which is
// what a counter that has never been incremented scrapes as anyway.
func aiCounter(t *testing.T, name string, want map[string]string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			if matchesLabels(m, want) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func TestAICredentialCooldownPolicy(t *testing.T) {
	for status, want := range map[int]time.Duration{
		401: 24 * time.Hour,
		403: 24 * time.Hour,
		402: time.Hour,
		429: time.Minute,
		500: 30 * time.Second,
		503: 30 * time.Second,
		400: 0,
	} {
		if got := aiCredentialCooldown(status); got != want {
			t.Errorf("status %d cooldown=%s want %s", status, got, want)
		}
	}
}

func TestAIRecordFailurePersistsCredentialCooldown(t *testing.T) {
	pool := testAICredPool(t)
	ctx := context.Background()
	var credentialID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO ai_gateway_key_credentials
		(gateway_key_id,provider,api_key_encrypted) VALUES (NULL,'sotamodel','\x74657374'::bytea) RETURNING id`).Scan(&credentialID); err != nil {
		t.Fatalf("insert credential: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM ai_gateway_key_credentials WHERE id=$1`, credentialID)
	})
	body, _ := json.Marshal(aiFailureRecordRequest{Provider: "sotamodel", Status: 429, CredentialID: credentialID.String()})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/internal/ai/failure/record", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	(&Handler{pool: pool}).AIRecordFailure(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var status string
	var remaining float64
	if err := pool.QueryRow(ctx, `SELECT status,extract(epoch FROM (unavailable_until-now())) FROM ai_gateway_key_credentials WHERE id=$1`, credentialID).Scan(&status, &remaining); err != nil {
		t.Fatalf("read cooldown: %v", err)
	}
	if status != "cooldown" || remaining < 50 || remaining > 65 {
		t.Fatalf("status=%q remaining=%v", status, remaining)
	}
}

func matchesLabels(m *dto.Metric, want map[string]string) bool {
	got := map[string]string{}
	for _, l := range m.GetLabel() {
		got[l.GetName()] = l.GetValue()
	}
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

func postAIFailure(t *testing.T, body string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/internal/ai/failure/record",
		bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	(&Handler{}).AIRecordFailure(c)
	return rec.Code
}

// TestARefusalIsCountedUnderTheGroupAndStatusReported is the end of the chain
// that was missing on 2026-08-04: the gateway saw the 402 and nothing on the
// platform side turned it into a number an alert could read.
func TestARefusalIsCountedUnderTheGroupAndStatusReported(t *testing.T) {
	labels := map[string]string{
		"model_group": "or-gpt-41-mini", "provider": "openrouter", "status": "402"}
	before := aiCounter(t, "dada_ai_upstream_failures_total", labels)

	code := postAIFailure(t, `{"model_group":"or-gpt-41-mini","provider":"openrouter",
		"status":402,"exception_type":"RateLimitError"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	if got := aiCounter(t, "dada_ai_upstream_failures_total", labels); got != before+1 {
		t.Fatalf("dada_ai_upstream_failures_total = %v, want %v", got, before+1)
	}
}

// TestAFallbackIsCountedSeparatelyFromARefusal guards the distinction the two
// alert rules rest on: a fallback means somebody still got an answer, so
// counting it as a failure would make the warning rule fire on a chain that is
// working exactly as designed.
func TestAFallbackIsCountedSeparatelyFromARefusal(t *testing.T) {
	fb := map[string]string{"requested": "or-gpt-41-mini", "served": "fast"}
	failAny := map[string]string{
		"model_group": "fast", "provider": "unknown", "status": "unknown"}
	beforeFB := aiCounter(t, "dada_ai_fallbacks_total", fb)
	beforeFail := aiCounter(t, "dada_ai_upstream_failures_total", failAny)

	if code := postAIFailure(t,
		`{"fallback":true,"requested":"or-gpt-41-mini","model_group":"fast"}`); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	if got := aiCounter(t, "dada_ai_fallbacks_total", fb); got != beforeFB+1 {
		t.Fatalf("dada_ai_fallbacks_total = %v, want %v", got, beforeFB+1)
	}
	if got := aiCounter(t, "dada_ai_upstream_failures_total", failAny); got != beforeFail {
		t.Fatalf("a fallback also counted as a failure: %v", got)
	}
}

// TestAnUnknownGroupCountsAsOtherRatherThanAsItself is the cardinality guard.
//
// The gateway is the only caller and it is behind the internal token, but these
// labels come out of a request body, and an unbounded label set is how a
// monitoring stack falls over during the outage it exists to report. An unknown
// name still has to count -- silence would hide a two-repo drift -- so it lands
// in a visible "other" bucket instead of being dropped.
func TestAnUnknownGroupCountsAsOtherRatherThanAsItself(t *testing.T) {
	other := map[string]string{
		"model_group": "other", "provider": "other", "status": "429"}
	before := aiCounter(t, "dada_ai_upstream_failures_total", other)

	postAIFailure(t, `{"model_group":"model-nobody-configured",
		"provider":"provider-nobody-configured","status":429}`)

	if got := aiCounter(t, "dada_ai_upstream_failures_total", other); got != before+1 {
		t.Fatalf("unknown names did not land in \"other\": %v", got)
	}
	verbatim := map[string]string{
		"model_group": "model-nobody-configured",
		"provider":    "provider-nobody-configured", "status": "429"}
	if got := aiCounter(t, "dada_ai_upstream_failures_total", verbatim); got != 0 {
		t.Fatalf("a body-supplied name became its own series: %v", got)
	}
}

// TestThePlatformsOwnProviderIsNotFiledAsOther catches the trap in reusing the
// BYOK provider list for this: nvidia_nim is deliberately absent from it,
// because a customer may not store a key for it -- and it is the provider
// behind every tier, so filing its failures under "other" would blind the
// metric to the platform's own outages.
func TestThePlatformsOwnProviderIsNotFiledAsOther(t *testing.T) {
	if got := aiProviderLabel(aiPlatformOwnedProvider); got != aiPlatformOwnedProvider {
		t.Fatalf("aiProviderLabel(%q) = %q", aiPlatformOwnedProvider, got)
	}
	if isKnownAIProvider(aiPlatformOwnedProvider) {
		t.Fatal("nvidia_nim is in the BYOK provider list; this test guards the case where it is not")
	}
}

// TestEveryAliasTheGatewayServesKeepsItsOwnSeries: the label bound is only
// useful if it admits the names actually in use. A catalog that drifted behind
// config.yaml would quietly collapse live groups into "other" and the alert
// would name a bucket instead of a provider.
func TestEveryAliasTheGatewayServesKeepsItsOwnSeries(t *testing.T) {
	for _, m := range aiCatalogModels {
		if got := aiMetricLabel(m.Alias); got != m.Alias {
			t.Errorf("aiMetricLabel(%q) = %q, want the alias itself", m.Alias, got)
		}
	}
	if got := aiMetricLabel(""); got != "" {
		t.Errorf("aiMetricLabel(\"\") = %q, want \"\" so the recorder decides", got)
	}
}
