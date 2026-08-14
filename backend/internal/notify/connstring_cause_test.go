package notify

import "testing"

// Fixture log excerpts below are trimmed real crash text, not invented
// shapes.
//
// phantomBaseLog reproduces the live megafactory incident: Node's
// pg-connection-string parses a scheme-less DATABASE_URL as a relative URL
// against its dummy base, so the crash names the phantom host "base" instead
// of the value the owner actually typed (pg-router.databases.svc.cluster.
// local). That value never appears in the log at all -- the classifier must
// still find it via the env var scan and fall through to the deterministic
// sorted-key pick.
//
// pgTranslateLog is Postgres/libpq's own DNS failure wording, seen from a
// Python/psycopg client instead of Node.
//
// appCodeLog is a genuine unrelated app bug (unhandled exception) with no
// DNS signature anywhere -- must not classify as a connection-string problem
// no matter what the env vars look like.
//
// validSchemeDnsLog pairs a real DNS failure with an env whose DATABASE_URL
// already carries a proper scheme -- a live network fault (the managed
// database's own host briefly unresolvable), which is NOT the bug this
// classifier exists to catch and must return ok=false.
const phantomBaseLog = `Error: getaddrinfo ENOTFOUND base
    at GetAddrInfoReqWrap.onlookup [as oncomplete] (node:dns:71:26) {
  errno: -3008,
  code: 'ENOTFOUND',
  syscall: 'getaddrinfo',
  hostname: 'base'
}
    at /app/node_modules/pg-pool/index.js:45:11`

const pgTranslateLog = `psycopg2.OperationalError: could not translate host name "rc1b-xxxx.mdb.example.net" to address: Name or service not known
`

const appCodeLog = `Traceback (most recent call last):
  File "app.py", line 12, in <module>
    raise ValueError("bad config")
ValueError: bad config`

const validSchemeDnsLog = `Error: getaddrinfo ENOTFOUND rc1b-xxxx.mdb.example.net
    at GetAddrInfoReqWrap.onlookup [as oncomplete] (node:dns:71:26) {
  errno: -3008,
  code: 'ENOTFOUND',
  syscall: 'getaddrinfo',
  hostname: 'rc1b-xxxx.mdb.example.net'
}`

func TestClassifyConnectionStringFailure_PhantomBase(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL": "pg-router.databases.svc.cluster.local",
		"NODE_ENV":     "production",
	}
	key, value, ok := ClassifyConnectionStringFailure(phantomBaseLog, env)
	if !ok {
		t.Fatalf("expected ok=true for phantom-base log, got false")
	}
	if key != "DATABASE_URL" {
		t.Errorf("key = %q, want DATABASE_URL", key)
	}
	if value != "pg-router.databases.svc.cluster.local" {
		t.Errorf("value = %q, want pg-router.databases.svc.cluster.local", value)
	}
}

func TestClassifyConnectionStringFailure_PostgresTranslateError(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL": "rc1b-xxxx.mdb.example.net",
	}
	key, value, ok := ClassifyConnectionStringFailure(pgTranslateLog, env)
	if !ok {
		t.Fatalf("expected ok=true for postgres translate-host-name log, got false")
	}
	if key != "DATABASE_URL" || value != "rc1b-xxxx.mdb.example.net" {
		t.Errorf("got key=%q value=%q", key, value)
	}
}

func TestClassifyConnectionStringFailure_AppCodeCrash(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL": "rc1b-xxxx.mdb.example.net",
	}
	_, _, ok := ClassifyConnectionStringFailure(appCodeLog, env)
	if ok {
		t.Fatalf("expected ok=false for a genuine app-code crash, got true")
	}
}

func TestClassifyConnectionStringFailure_ValidSchemeRealNetworkFault(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL": "postgresql://svc-app:secret@rc1b-xxxx.mdb.example.net:5432/appdb",
	}
	_, _, ok := ClassifyConnectionStringFailure(validSchemeDnsLog, env)
	if ok {
		t.Fatalf("expected ok=false when DATABASE_URL already has a scheme (real network fault, not this bug), got true")
	}
}

func TestClassifyConnectionStringFailure_NoDNSSignature(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL": "rc1b-xxxx.mdb.example.net",
	}
	_, _, ok := ClassifyConnectionStringFailure("some unrelated crash text with no DNS words", env)
	if ok {
		t.Fatalf("expected ok=false with no DNS-failure signature in the log, got true")
	}
}

func TestClassifyConnectionStringFailure_NoCandidateKey(t *testing.T) {
	env := map[string]string{
		"NODE_ENV": "production",
		"PORT":     "3000",
	}
	_, _, ok := ClassifyConnectionStringFailure(phantomBaseLog, env)
	if ok {
		t.Fatalf("expected ok=false when no env var looks like a bare connection string, got true")
	}
}

func TestClassifyConnectionStringFailure_DeterministicMultiCandidate(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL": "pg-router.databases.svc.cluster.local",
		"REDIS_URL":    "redis-router.databases.svc.cluster.local",
	}
	key1, _, ok1 := ClassifyConnectionStringFailure(phantomBaseLog, env)
	key2, _, ok2 := ClassifyConnectionStringFailure(phantomBaseLog, env)
	if !ok1 || !ok2 {
		t.Fatalf("expected ok=true on both calls")
	}
	if key1 != key2 {
		t.Fatalf("classification flapped across calls: %q vs %q", key1, key2)
	}
	if key1 != "DATABASE_URL" {
		t.Errorf("expected deterministic alphabetical pick DATABASE_URL, got %q", key1)
	}
}
