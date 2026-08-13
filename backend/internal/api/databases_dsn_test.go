package api

import "testing"

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
			want:     "postgresql://app:s3cr3t@pg-router.databases.svc.cluster.local:5432/megafactory",
		},
		{
			name:     "percent-encodes credentials that would break the url",
			user:     "app@tenant",
			pass:     "p/a:s s@",
			host:     "db.internal",
			port:     "5432",
			database: "app",
			want:     "postgresql://app%40tenant:p%2Fa%3As%20s%40@db.internal:5432/app",
		},
		{
			name:     "defaults the port",
			user:     "app",
			pass:     "x",
			host:     "db.internal",
			database: "app",
			want:     "postgresql://app:x@db.internal:5432/app",
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
