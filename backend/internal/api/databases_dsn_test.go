package api

import (
	"strings"
	"testing"
)

func TestPostgresDSN(t *testing.T) {
	cases := []struct {
		name                                   string
		user, pass, host, port, database, want string
	}{
		{
			name:     "assembles a libpq url",
			user:     "app",
			pass:     "s3cr3t",
			host:     "pg-router.databases.svc.cluster.local",
			port:     "5432",
			database: "megafactory",
			want:     "postgresql://app:s3cr3t@pg-router.databases.svc.cluster.local:5432/megafactory?sslmode=disable",
		},
		{
			name:     "percent-encodes credentials that would break the url",
			user:     "app@tenant",
			pass:     "p/a:s s@",
			host:     "db.internal",
			port:     "5432",
			database: "app",
			want:     "postgresql://app%40tenant:p%2Fa%3As%20s%40@db.internal:5432/app?sslmode=disable",
		},
		{
			name:     "defaults the port",
			user:     "app",
			pass:     "x",
			host:     "db.internal",
			database: "app",
			want:     "postgresql://app:x@db.internal:5432/app?sslmode=disable",
		},
		{
			name:     "yields nothing without a host",
			user:     "app",
			pass:     "x",
			database: "app",
			want:     "",
		},
		{
			name: "yields nothing without a database name",
			user: "app",
			pass: "x",
			host: "db.internal",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := postgresDSN(tc.user, tc.pass, tc.host, tc.port, tc.database)
			if got != tc.want {
				t.Fatalf("postgresDSN = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPostgresDSNCarriesExplicitSSLMode guards the megafactory incident:
// pg-router has no client_tls_sslmode configured (verified live 2026-08-14
// against both the transaction and session pools with psql sslmode=require,
// which failed with "server does not support SSL, but SSL was required").
// Client libraries that default to requesting TLS then crash loop on a DSN
// that leaves sslmode unstated, so postgresDSN must always spell it out.
func TestPostgresDSNCarriesExplicitSSLMode(t *testing.T) {
	dsn := postgresDSN("app", "x", "pg-router.databases.svc.cluster.local", "5432", "megafactory")
	if !strings.Contains(dsn, "sslmode=disable") {
		t.Fatalf("postgresDSN = %q, want it to carry sslmode=disable", dsn)
	}
}

// TestPostgresDSNTLSFlagOff pins that MANAGED_DB_TLS_DSN_ENABLED unset (the
// default, and the state of every environment until the paired infra work --
// pg-router TLS listener, LE cert, gitops-agent hostAliases rollout -- is
// confirmed live) renders byte-identical output to before this change existed,
// for both the bare in-cluster host and any other host.
func TestPostgresDSNTLSFlagOff(t *testing.T) {
	dsn := postgresDSN("app", "x", "pg-router.databases.svc.cluster.local", "5432", "megafactory")
	want := "postgresql://app:x@pg-router.databases.svc.cluster.local:5432/megafactory?sslmode=disable"
	if dsn != want {
		t.Fatalf("postgresDSN = %q, want %q (flag must default off)", dsn, want)
	}
}

// TestPostgresDSNTLSFlagOnRewritesOnlyTheInternalHost proves the rewrite only
// fires for the exact bare-in-cluster endpoint, so an externally-routed host
// (already public, unrelated to this cert) is never touched even with the
// flag on.
func TestPostgresDSNTLSFlagOnRewritesOnlyTheInternalHost(t *testing.T) {
	t.Setenv("MANAGED_DB_TLS_DSN_ENABLED", "true")

	dsn := postgresDSN("app", "x", "pg-router.databases.svc.cluster.local", "5432", "megafactory")
	want := "postgresql://app:x@db.pv.dada-tuda.ru:5432/megafactory?sslmode=require"
	if dsn != want {
		t.Fatalf("postgresDSN = %q, want %q", dsn, want)
	}

	external := postgresDSN("app", "x", "db-external.dada-tuda.ru", "5432", "megafactory")
	wantExternal := "postgresql://app:x@db-external.dada-tuda.ru:5432/megafactory?sslmode=disable"
	if external != wantExternal {
		t.Fatalf("postgresDSN(external) = %q, want %q (only the bare in-cluster host is rewritten)", external, wantExternal)
	}
}

// TestManagedDBEffectiveHostAgreesWithTheDSN pins the two halves of the reveal
// payload together. The host field and the dsn field are read by the same user
// in the same glance, and a user who copies the host into their own connection
// string must land on the same endpoint the dsn would have given them.
func TestManagedDBEffectiveHostAgreesWithTheDSN(t *testing.T) {
	t.Setenv("MANAGED_DB_TLS_DSN_ENABLED", "true")

	host := managedDBEffectiveHost("pg-router.databases.svc.cluster.local")
	if host != "db.pv.dada-tuda.ru" {
		t.Fatalf("managedDBEffectiveHost = %q, want the TLS hostname the dsn uses", host)
	}
	if mode := managedDBEffectiveSSLMode(host); mode != "require" {
		t.Fatalf("managedDBEffectiveSSLMode = %q, want require", mode)
	}
	if again := managedDBEffectiveHost(host); again != host {
		t.Fatalf("managedDBEffectiveHost is not idempotent: %q -> %q", host, again)
	}

	external := managedDBEffectiveHost("db-external.dada-tuda.ru")
	if external != "db-external.dada-tuda.ru" {
		t.Fatalf("managedDBEffectiveHost(external) = %q, want it untouched", external)
	}
	if mode := managedDBEffectiveSSLMode(external); mode != "disable" {
		t.Fatalf("managedDBEffectiveSSLMode(external) = %q, want disable", mode)
	}
}

// TestManagedDBEffectiveHostStaysInternalWhileFlagOff proves the host field
// cannot advertise a name the infra has not been confirmed to serve.
func TestManagedDBEffectiveHostStaysInternalWhileFlagOff(t *testing.T) {
	t.Setenv("MANAGED_DB_TLS_DSN_ENABLED", "")

	host := managedDBEffectiveHost("pg-router.databases.svc.cluster.local")
	if host != "pg-router.databases.svc.cluster.local" {
		t.Fatalf("managedDBEffectiveHost = %q, want the internal host while the flag is off", host)
	}
	if mode := managedDBEffectiveSSLMode(host); mode != "disable" {
		t.Fatalf("managedDBEffectiveSSLMode = %q, want disable", mode)
	}
}
