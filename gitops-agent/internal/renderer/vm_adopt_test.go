package renderer

import (
	"strings"
	"testing"
)

func TestClassifyResource(t *testing.T) {
	cases := map[string]ResourceKind{
		"mirror.gcr.io/library/postgres:16-alpine": KindServiceDatabase,
		"redis:7": KindServiceDatabase,
		"mirror.gcr.io/library/nginx:1.27-alpine": KindIngress,
		"traefik:v3": KindIngress,
		"nexus.dada-tuda.ru/dada/profi-backend:master-1.0.0-194": KindApplication,
	}
	for img, want := range cases {
		if got := ClassifyResource(img); got != want {
			t.Errorf("ClassifyResource(%q) = %s, want %s", img, got, want)
		}
	}
}

// findata's discovered postgres → a ServiceDatabase spec matching the live DB.
func TestBuildVMDatabaseSpec_Findata(t *testing.T) {
	spec := BuildVMDatabaseSpec(DiscoveredService{
		Name:    "postgres",
		Image:   "mirror.gcr.io/library/postgres:16-alpine",
		Env:     map[string]string{"POSTGRES_DB": "feedback", "POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": "pswd"},
		Ports:   []string{"65433:5432/tcp"},
		Volumes: []string{"compose_profi_pg_data:/var/lib/postgresql/data"},
	})
	if spec.Version != "16" || spec.Database != "feedback" || spec.User != "postgres" {
		t.Errorf("db spec meta wrong: %+v", spec)
	}
	if spec.VolumeName != "compose_profi_pg_data" {
		t.Errorf("external volume = %q, want compose_profi_pg_data (data-safety)", spec.VolumeName)
	}
	if spec.HostPort != 65433 {
		t.Errorf("host port = %d, want 65433", spec.HostPort)
	}
}

// findata's discovered nginx → an Ingress spec matching the live routing/TLS.
func TestBuildVMIngressSpec_Findata(t *testing.T) {
	spec, notes := BuildVMIngressSpec(DiscoveredService{
		Name:  "nginx",
		Image: "mirror.gcr.io/library/nginx:1.27-alpine",
		Env: map[string]string{
			"DOMAIN":              "fin-data.pro",
			"BACKEND_UPSTREAM":    "backend:8001",
			"FRONTEND_UPSTREAM":   "frontend:5173",
			"NGINX_SSL_CERT_PATH": "/etc/nginx/certs/live/fin-data.pro/fullchain.pem",
			"NGINX_SSL_KEY_PATH":  "/etc/nginx/certs/live/fin-data.pro/privkey.pem",
		},
	})
	if spec.Host != "fin-data.pro" || len(spec.Aliases) != 1 || spec.Aliases[0] != "www.fin-data.pro" {
		t.Errorf("host/aliases wrong: %+v", spec)
	}
	if !spec.TLS.Enabled || spec.TLS.CertPath != "/etc/nginx/certs/live/fin-data.pro/fullchain.pem" {
		t.Errorf("tls wrong: %+v", spec.TLS)
	}
	if len(spec.Rules) != 2 ||
		spec.Rules[0] != (VMIngressRule{Path: "/api/", App: "backend", Port: 8001}) ||
		spec.Rules[1] != (VMIngressRule{Path: "/", App: "frontend", Port: 5173}) {
		t.Errorf("rules wrong: %+v", spec.Rules)
	}
	if len(notes) == 0 {
		t.Error("expected review notes (routing paths + basic-auth are assumptions)")
	}
}

// End-to-end: discovery → spec → render reproduces findata's live config.
func TestAdoptToRender_Findata(t *testing.T) {
	// nginx: discover → Ingress spec → nginx conf.
	ingSpec, _ := BuildVMIngressSpec(DiscoveredService{
		Name: "nginx", Image: "nginx:1.27-alpine",
		Env: map[string]string{
			"DOMAIN": "fin-data.pro", "BACKEND_UPSTREAM": "backend:8001",
			"FRONTEND_UPSTREAM":   "frontend:5173",
			"NGINX_SSL_CERT_PATH": "/etc/nginx/certs/live/fin-data.pro/fullchain.pem",
			"NGINX_SSL_KEY_PATH":  "/etc/nginx/certs/live/fin-data.pro/privkey.pem",
		},
	})
	conf := RenderNginxConf(ingSpec)
	for _, w := range []string{"server_name fin-data.pro;", "proxy_pass http://backend:8001;", "proxy_pass http://frontend:5173;"} {
		if !strings.Contains(conf, w) {
			t.Errorf("adopt→render nginx missing %q", w)
		}
	}

	// postgres: discover → DB spec → compose service.
	dbSpec := BuildVMDatabaseSpec(DiscoveredService{
		Name: "postgres", Image: "postgres:16-alpine",
		Env:     map[string]string{"POSTGRES_DB": "feedback", "POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": "pswd"},
		Volumes: []string{"compose_profi_pg_data:/var/lib/postgresql/data"},
	})
	svc, vol, _ := RenderPostgresService(dbSpec)
	if vol != "compose_profi_pg_data" || !strings.Contains(svc, "POSTGRES_DB: feedback") {
		t.Errorf("adopt→render postgres wrong:\n%s", svc)
	}
}
