package worker

import "testing"

func TestClassifyService(t *testing.T) {
	cases := []struct {
		image               string
		kind, subtype, name string
	}{
		{"nexus.dada-tuda.ru/dada/profi-backend:master-1.0.0-194", "App", "", "profi-backend"},
		{"nexus.dada-tuda.ru/dada/profi:master-1.0.0-174", "App", "", "profi"},
		{"mirror.gcr.io/library/postgres:16-alpine", "Infra", "database", "postgres"},
		{"mirror.gcr.io/library/nginx:1.27-alpine", "Infra", "proxy", "nginx"},
		{"redis:7", "Infra", "cache", "redis"},
		{"rabbitmq:3-management", "Infra", "queue", "rabbitmq"},
		{"ghcr.io/org/app@sha256:abc123", "App", "", "app"},
	}
	for _, c := range cases {
		k, st, n := classifyService(c.image)
		if k != c.kind || st != c.subtype || n != c.name {
			t.Errorf("classifyService(%q) = (%q,%q,%q), want (%q,%q,%q)",
				c.image, k, st, n, c.kind, c.subtype, c.name)
		}
	}
}
