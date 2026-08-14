package api

import "testing"

// TestValidEnvKey pins the server-side key check that bulk entry depends on.
// The single-variable form validates in the browser via a pattern attribute;
// a pasted .env reaches the server as free text, so a key like "MY KEY" or
// "9LIVES" must be rejected here rather than stored and then silently ignored
// by the container runtime.
func TestValidEnvKey(t *testing.T) {
	valid := []string{"A", "_", "BOT_TOKEN", "_private", "PORT8080", "a1_B2"}
	for _, k := range valid {
		if !validEnvKey(k) {
			t.Errorf("validEnvKey(%q) = false, want true", k)
		}
	}

	invalid := []string{"", "9LIVES", "MY KEY", "MY-KEY", "KEY=VALUE", "KÖLN", "a.b", "#comment"}
	for _, k := range invalid {
		if validEnvKey(k) {
			t.Errorf("validEnvKey(%q) = true, want false", k)
		}
	}

	long := make([]byte, 257)
	for i := range long {
		long[i] = 'A'
	}
	if validEnvKey(string(long)) {
		t.Error("validEnvKey(257 chars) = true, want false")
	}
}

// TestConnectionStringWarning pins the detector behind the DATABASE_URL
// incident: a bare host copied from the database page (no scheme://) saved
// under a connection-string-shaped key must produce a warning, but nothing
// else does -- unrelated keys, valid DSNs, and empty values must stay silent.
func TestConnectionStringWarning(t *testing.T) {
	cases := []struct {
		name     string
		key      string
		value    string
		wantWarn bool
	}{
		{
			name:     "bare host copied from the database page",
			key:      "DATABASE_URL",
			value:    "pg-router.databases.svc.cluster.local",
			wantWarn: true,
		},
		{
			name:     "valid postgres DSN",
			key:      "DATABASE_URL",
			value:    "postgresql://app:secret@pg-router.databases.svc.cluster.local:5432/megafactory",
			wantWarn: false,
		},
		{
			name:     "host with port but no scheme",
			key:      "DATABASE_URL",
			value:    "pg-router.databases.svc.cluster.local:5432",
			wantWarn: true,
		},
		{
			name:     "key unrelated to a connection",
			key:      "BOT_TOKEN",
			value:    "pg-router.databases.svc.cluster.local",
			wantWarn: false,
		},
		{
			name:     "empty value",
			key:      "DATABASE_URL",
			value:    "",
			wantWarn: false,
		},
		{
			name:     "redis DSN with scheme",
			key:      "REDIS_URL",
			value:    "redis://default:secret@redis.databases.svc.cluster.local:6379/0",
			wantWarn: false,
		},
		{
			name:     "suffix-matched custom key without scheme",
			key:      "REPORTING_DB_DSN",
			value:    "reporting.databases.svc.cluster.local",
			wantWarn: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := connectionStringWarning(tc.key, tc.value)
			if tc.wantWarn && got == nil {
				t.Fatalf("connectionStringWarning(%q, %q) = nil, want a warning", tc.key, tc.value)
			}
			if !tc.wantWarn && got != nil {
				t.Fatalf("connectionStringWarning(%q, %q) = %+v, want nil", tc.key, tc.value, *got)
			}
			if got != nil && got.Code != "value_is_not_a_connection_string" {
				t.Errorf("warning code = %q, want value_is_not_a_connection_string", got.Code)
			}
		})
	}
}
