package notify

import "testing"

func TestClassifySSLNotSupported_MatchesLogAndEnv(t *testing.T) {
	logExcerpt := "[server] Не удалось стартовать: Error: The server does not support SSL connections\n    at pg-pool/index.js:45"
	env := map[string]string{
		"DATABASE_URL": "postgresql://svc-megafactory:secret@pg-router.databases.svc.cluster.local:5432/megafactory",
	}

	key, value, ok := ClassifySSLNotSupported(logExcerpt, env)
	if !ok {
		t.Fatalf("expected match, got ok=false")
	}
	if key != "DATABASE_URL" {
		t.Errorf("key = %q, want DATABASE_URL", key)
	}
	if value != env["DATABASE_URL"] {
		t.Errorf("value = %q, want %q", value, env["DATABASE_URL"])
	}
}

func TestClassifySSLNotSupported_NoMatchWithoutSignature(t *testing.T) {
	logExcerpt := "connection refused"
	env := map[string]string{
		"DATABASE_URL": "postgresql://user:pass@host:5432/db",
	}
	if _, _, ok := ClassifySSLNotSupported(logExcerpt, env); ok {
		t.Fatalf("expected no match without the SSL signature")
	}
}

func TestClassifySSLNotSupported_NoMatchWithoutScheme(t *testing.T) {
	logExcerpt := "The server does not support SSL connections"
	env := map[string]string{
		"DATABASE_URL": "rc1b-xxxx.mdb.example.net",
	}
	if _, _, ok := ClassifySSLNotSupported(logExcerpt, env); ok {
		t.Fatalf("expected no match for a scheme-less value -- that is ClassifyConnectionStringFailure's job")
	}
}

func TestClassifySSLNotSupported_SkipsValueThatAlreadyHasSSLMode(t *testing.T) {
	logExcerpt := "The server does not support SSL connections"
	env := map[string]string{
		"DATABASE_URL": "postgresql://user:pass@host:5432/db?sslmode=disable",
	}
	if _, _, ok := ClassifySSLNotSupported(logExcerpt, env); ok {
		t.Fatalf("expected no match once sslmode is already set -- nothing left to fix")
	}
}

func TestClassifySSLNotSupported_DeterministicAcrossMultipleCandidates(t *testing.T) {
	logExcerpt := "The server does not support SSL connections"
	env := map[string]string{
		"REDIS_URL":    "redis://user:pass@host:6379/0",
		"DATABASE_URL": "postgresql://user:pass@host:5432/db",
	}
	key, _, ok := ClassifySSLNotSupported(logExcerpt, env)
	if !ok {
		t.Fatalf("expected match")
	}
	if key != "DATABASE_URL" {
		t.Errorf("key = %q, want DATABASE_URL (sorted first)", key)
	}
}

func TestSuggestSSLModeDisable_AppendsToBareDSN(t *testing.T) {
	got := SuggestSSLModeDisable("postgresql://svc:pw@pg-router.databases.svc.cluster.local:5432/megafactory")
	want := "postgresql://svc:pw@pg-router.databases.svc.cluster.local:5432/megafactory?sslmode=disable"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSuggestSSLModeDisable_PreservesExistingQueryParams(t *testing.T) {
	got := SuggestSSLModeDisable("postgresql://svc:pw@host:5432/db?application_name=megafactory&connect_timeout=5")
	want := "postgresql://svc:pw@host:5432/db?application_name=megafactory&connect_timeout=5&sslmode=disable"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSuggestSSLModeDisable_HandlesTrailingQuestionMark(t *testing.T) {
	got := SuggestSSLModeDisable("postgresql://svc:pw@host:5432/db?")
	want := "postgresql://svc:pw@host:5432/db?sslmode=disable"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSuggestSSLModeDisable_HandlesTrailingAmpersand(t *testing.T) {
	got := SuggestSSLModeDisable("postgresql://svc:pw@host:5432/db?connect_timeout=5&")
	want := "postgresql://svc:pw@host:5432/db?connect_timeout=5&sslmode=disable"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSuggestSSLModeDisable_IdempotentWhenAlreadyDisabled(t *testing.T) {
	dsn := "postgresql://svc:pw@host:5432/db?sslmode=disable"
	got := SuggestSSLModeDisable(dsn)
	if got != dsn {
		t.Errorf("got %q, want unchanged %q", got, dsn)
	}
	got2 := SuggestSSLModeDisable(got)
	if got2 != dsn {
		t.Errorf("second call: got %q, want unchanged %q", got2, dsn)
	}
}

func TestSuggestSSLModeDisable_IdempotentWhenSSLModeIsNotTheFirstParam(t *testing.T) {
	dsn := "postgresql://svc:pw@host:5432/db?connect_timeout=5&sslmode=disable"
	got := SuggestSSLModeDisable(dsn)
	if got != dsn {
		t.Errorf("got %q, want unchanged %q", got, dsn)
	}
}

func TestSuggestSSLModeDisable_DetectsSSLModeCaseInsensitively(t *testing.T) {
	dsn := "postgresql://svc:pw@host:5432/db?SSLMode=require"
	got := SuggestSSLModeDisable(dsn)
	if got != dsn {
		t.Errorf("got %q, want unchanged %q (an existing sslmode, even wrong, is left to the user, not silently overridden)", got, dsn)
	}
}
