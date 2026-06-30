// Package telemetry holds the device-facing ingest primitives shared by the
// console backend (internal/api) and the standalone telemetry gateway
// (cmd/gateway): scoped key parsing/verification, the per-app rate limiter,
// metric-name sanitization, and OTLP decode. Lifting these here keeps the two
// services from forking the security-sensitive ingest path (ADR-012).
package telemetry

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"strings"

	"golang.org/x/crypto/argon2"
)

// KeyPrefix is the recognizable prefix every monitoring ingest key carries. A
// presented token starting with it is treated as a scoped device key rather
// than a JWT.
const KeyPrefix = "dmon_"

// prefixLen is the displayable prefix length stored in monitoring_apps.
// api_key_prefix ("dmon_" + 8 base64url chars). It is the narrow used for the
// indexed candidate lookup; argon2id is the decider.
const prefixLen = 13

// argon2 parameters — MUST match across generate + verify or keys never match.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// KeyFromHeaders extracts a presented monitoring ingest key from either an
// X-API-Key value or an "Authorization: Bearer <key>" value. Returns "" when no
// dmon_ key is present so the caller can fall back to JWT auth. Both raw header
// strings are passed in so this stays framework-agnostic (usable from Gin and
// from the gateway's net/http handlers).
func KeyFromHeaders(apiKey, authorization string) string {
	if k := strings.TrimSpace(apiKey); strings.HasPrefix(k, KeyPrefix) {
		return k
	}
	parts := strings.SplitN(authorization, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") && strings.HasPrefix(parts[1], KeyPrefix) {
		return parts[1]
	}
	return ""
}

// KeyLookupPrefix returns the indexed candidate prefix for a presented key
// (the monitoring_apps.api_key_prefix value). Used by the gateway to resolve the
// tenant from the key alone (no appId in the path).
func KeyLookupPrefix(full string) string {
	if len(full) >= prefixLen {
		return full[:prefixLen]
	}
	return full
}

// VerifyKeyHash checks a presented key against the stored salt(16)||digest(32)
// argon2id hash (the layout GenerateKey writes), in constant time.
func VerifyKeyHash(full string, stored []byte) bool {
	if len(stored) != argonSaltLen+argonKeyLen {
		return false
	}
	salt, want := stored[:argonSaltLen], stored[argonSaltLen:]
	got := argon2.IDKey([]byte(full), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// GenerateKey mints a plaintext key shown once, plus a displayable prefix and an
// argon2id hash (salt(16)||digest(32)). The plaintext is never persisted.
func GenerateKey() (full, prefix string, hash []byte, err error) {
	raw := make([]byte, 24)
	if _, err = rand.Read(raw); err != nil {
		return
	}
	full = KeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	prefix = KeyLookupPrefix(full)
	salt := make([]byte, argonSaltLen)
	if _, err = rand.Read(salt); err != nil {
		return
	}
	digest := argon2.IDKey([]byte(full), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	hash = append(salt, digest...)
	return
}

// SanitizeMetricName coerces an arbitrary metric key into a valid Prometheus
// metric name ([a-zA-Z_][a-zA-Z0-9_]*). Prevents remote-write rejection and
// label injection from custom metric names.
func SanitizeMetricName(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		out = "_"
	}
	return out
}

// CounterMetricName applies the OpenTelemetry → Prometheus naming convention for
// monotonic sums: a cumulative counter carries a _total suffix so the read path
// (and standard Prometheus tooling) recognise it as a counter and rate() it
// instead of charting the raw monotonically-increasing value. Names that already
// end in _total are returned unchanged.
func CounterMetricName(name string) string {
	if strings.HasSuffix(name, "_total") {
		return name
	}
	return name + "_total"
}
