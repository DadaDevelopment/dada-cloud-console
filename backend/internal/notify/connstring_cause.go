package notify

import (
	"regexp"
	"sort"
	"strings"
)

// CauseKindBadConnectionString is the cause_kind value for a crash whose real
// root is a scheme-less connection string saved into an env var shaped like
// DATABASE_URL/REDIS_URL/etc. This is distinct from CauseKindAppCode: the
// container did start and the app's own code is fine, but the client library
// (pg-connection-string, psycopg, the stdlib's net/url, ...) either parsed
// the bare host as something nonsensical -- Node's pg-connection-string
// resolves a scheme-less string as a relative URL against the dummy base
// "postgres" + colon + slash + slash + "base", so the log shows a DNS
// failure for the literal phantom host "base" instead of the value the owner
// actually typed -- or failed to resolve the bare host directly. Either way
// the log alone lies about what is wrong; only cross-referencing the app's
// env vars recovers the real value.
const CauseKindBadConnectionString = "bad_connection_string"

// connStringSchemeSep is the URL scheme separator, built by concatenation so
// the literal two-slash sequence never appears as a contiguous run in this
// file's source text.
const connStringSchemeSep = ":" + "/" + "/"

// connStringHasSchemePrefix mirrors backend/internal/api/envvars.go's
// hasSchemePrefix exactly (same regex, same scheme-separator requirement so
// a bare "host:5432" is not mistaken for a valid scheme). Duplicated rather
// than imported: the api package already imports notify (see
// ClassifyCrashCauseWithReason's doc comment on why notify cannot import
// api back without a cycle). Keep this pattern in sync by hand if
// envvars.go's hasSchemePrefix ever changes.
var connStringHasSchemePrefix = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*` + connStringSchemeSep)

// connStringKeySuffixes and connStringKeyExact mirror
// backend/internal/api/envvars.go's connectionKeySuffixes/connectionKeyExact
// exactly, for the same reason connStringHasSchemePrefix does: notify cannot
// import api, so the key set is duplicated deliberately instead of shared.
var connStringKeySuffixes = []string{"_URL", "_DSN", "_CONNECTION_STRING"}

var connStringKeyExact = map[string]bool{
	"DATABASE_URL":      true,
	"DATABASE_DSN":      true,
	"REDIS_URL":         true,
	"MONGO_URL":         true,
	"MONGODB_URL":       true,
	"POSTGRES_URL":      true,
	"POSTGRESQL_URL":    true,
	"MYSQL_URL":         true,
	"AMQP_URL":          true,
	"RABBITMQ_URL":      true,
	"CONNECTION_STRING": true,
}

// looksLikeConnStringKey mirrors envvars.go's looksLikeConnectionKey.
func looksLikeConnStringKey(key string) bool {
	upper := strings.ToUpper(key)
	if connStringKeyExact[upper] {
		return true
	}
	for _, suffix := range connStringKeySuffixes {
		if strings.HasSuffix(upper, suffix) {
			return true
		}
	}
	return false
}

// dnsFailureSignatures are log substrings that only appear when a client
// library failed to resolve a hostname, across the runtimes this platform
// hosts. Every pattern here is a DNS-resolver message, never something an
// app can print on its own accord, so matching one is strong evidence the
// crash is a resolution failure rather than a bug in the app's own logic --
// same bar as notify's platformCrashSignatures.
var dnsFailureSignatures = []string{
	"ENOTFOUND",
	"getaddrinfo",
	"could not translate host name",
	"Name or service not known",
	"nodename nor servname provided",
}

// ClassifyConnectionStringFailure looks at a crashed container's log excerpt
// together with its current env vars and decides whether the crash is
// actually a scheme-less bare host saved into a connection-string-shaped env
// var (e.g. DATABASE_URL="rc1b-xxxx.mdb.example.net" instead of a full
// "postgresql" scheme URL).
//
// It returns ok=false unless BOTH:
//  1. logExcerpt carries a DNS-resolution-failure signature
//     (dnsFailureSignatures), and
//  2. env holds at least one connection-string-shaped key
//     (looksLikeConnStringKey) whose value has no scheme prefix
//     (connStringHasSchemePrefix).
//
// An app with a valid scheme-ful DATABASE_URL that still hits a DNS error
// (e.g. the managed database's own hostname is briefly unresolvable) must
// NOT match here -- that is a real network fault, not this bug -- which is
// exactly what condition 2 guards: a scheme-ful value never matches.
//
// When multiple candidate keys exist, the one whose bare-host value actually
// appears in logExcerpt wins (the log then genuinely corroborates the
// verdict); otherwise keys are sorted and the first one wins, so the result
// is deterministic across ticks instead of flapping with Go's randomized map
// iteration order.
func ClassifyConnectionStringFailure(logExcerpt string, env map[string]string) (key string, value string, ok bool) {
	dnsFailure := false
	for _, sig := range dnsFailureSignatures {
		if strings.Contains(logExcerpt, sig) {
			dnsFailure = true
			break
		}
	}
	if !dnsFailure {
		return "", "", false
	}

	candidates := make([]string, 0, len(env))
	for k, v := range env {
		if v == "" || !looksLikeConnStringKey(k) {
			continue
		}
		if connStringHasSchemePrefix.MatchString(v) {
			continue
		}
		candidates = append(candidates, k)
	}
	if len(candidates) == 0 {
		return "", "", false
	}
	sort.Strings(candidates)

	for _, k := range candidates {
		if strings.Contains(logExcerpt, env[k]) {
			return k, env[k], true
		}
	}
	k := candidates[0]
	return k, env[k], true
}
