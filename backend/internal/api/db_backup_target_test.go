package api

import "testing"

func TestStatefulSetNameFromHostTakesTheServiceLabel(t *testing.T) {
	got := statefulSetNameFromHost("pg-shard-0-postgresql.databases.svc.cluster.local")
	if got != "pg-shard-0-postgresql" {
		t.Fatalf("statefulSetNameFromHost = %q, want pg-shard-0-postgresql", got)
	}
}

func TestStatefulSetNameFromHostAcceptsABareName(t *testing.T) {
	if got := statefulSetNameFromHost("postgresql"); got != "postgresql" {
		t.Fatalf("statefulSetNameFromHost = %q, want postgresql", got)
	}
}

func TestStatefulSetNameFromHostRejectsAnythingButADNSLabel(t *testing.T) {
	for _, host := range []string{
		"",
		"   ",
		".databases.svc",
		"Postgresql",
		"pg_shard_0",
		"-postgresql",
		"postgresql-",
		"pg shard",
		"pg/../secret",
	} {
		if got := statefulSetNameFromHost(host); got != "" {
			t.Fatalf("statefulSetNameFromHost(%q) = %q, want empty", host, got)
		}
	}
}
