package renderer

import (
	"strings"
	"testing"
)

// findata's live Ingress, expressed as a spec, must generate nginx config
// equivalent to the hand-authored template read live from the VM.
func TestRenderNginxConf_ReproducesFindata(t *testing.T) {
	conf := RenderNginxConf(VMIngressSpec{
		Host:        "fin-data.pro",
		Aliases:     []string{"www.fin-data.pro"},
		SSLRedirect: true,
		TLS: VMIngressTLS{
			Enabled:    true,
			MinVersion: "1.2",
			CertPath:   "/etc/nginx/certs/live/fin-data.pro/fullchain.pem",
			KeyPath:    "/etc/nginx/certs/live/fin-data.pro/privkey.pem",
		},
		BasicAuth: "/etc/nginx/.htpasswd",
		Rules: []VMIngressRule{
			{Path: "/api/", App: "backend", Port: 8001},
			{Path: "/", App: "frontend", Port: 5173},
		},
	})
	want := []string{
		"server_name fin-data.pro;",
		"server_name www.fin-data.pro;",
		"return 301 https://fin-data.pro$request_uri;", // www + http→https redirects
		"listen 443 ssl http2;",
		"ssl_certificate /etc/nginx/certs/live/fin-data.pro/fullchain.pem;",
		"ssl_protocols TLSv1.2 TLSv1.3;",
		"ssl_ciphers HIGH:!aNULL:!MD5;",
		`auth_basic "Private area";`,
		"auth_basic_user_file /etc/nginx/.htpasswd;",
		"location /api/ {",
		"proxy_pass http://backend:8001;",
		"location / {",
		"proxy_pass http://frontend:5173;",
		"proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;",
	}
	for _, w := range want {
		if !strings.Contains(conf, w) {
			t.Errorf("generated nginx conf missing %q\n---\n%s", w, conf)
		}
	}
	// /api/ must be routed before /, else / swallows everything (order preserved).
	if strings.Index(conf, "location /api/") > strings.Index(conf, "location / {") {
		t.Error("more-specific /api/ location must render before the catch-all /")
	}
}

// A custom domain (ExtraHost) attached to an ingress must SERVE traffic to its
// own app:port with its own cert, alongside (not instead of) the canonical
// Host -- unlike Aliases, which only 301-redirect to Host.
func TestRenderNginxConf_ExtraHost(t *testing.T) {
	conf := RenderNginxConf(VMIngressSpec{
		Host:        "fin-data.pro",
		SSLRedirect: true,
		TLS: VMIngressTLS{
			Enabled:    true,
			MinVersion: "1.2",
			CertPath:   "/etc/nginx/certs/live/fin-data.pro/fullchain.pem",
			KeyPath:    "/etc/nginx/certs/live/fin-data.pro/privkey.pem",
		},
		Rules: []VMIngressRule{{Path: "/", App: "frontend", Port: 5173}},
		ExtraHosts: []VMExtraHost{
			{
				Host:     "custom.example.com",
				CertPath: "/etc/nginx/certs/live/custom.example.com/fullchain.pem",
				KeyPath:  "/etc/nginx/certs/live/custom.example.com/privkey.pem",
				App:      "custom-app",
				Port:     9000,
				TLSReady: true,
			},
		},
	})
	for _, w := range []string{
		"server_name fin-data.pro;",
		"proxy_pass http://frontend:5173;",
		"server_name custom.example.com;",
		"ssl_certificate /etc/nginx/certs/live/custom.example.com/fullchain.pem;",
		"ssl_certificate_key /etc/nginx/certs/live/custom.example.com/privkey.pem;",
		"proxy_pass http://custom-app:9000;",
	} {
		if !strings.Contains(conf, w) {
			t.Errorf("generated nginx conf missing %q\n---\n%s", w, conf)
		}
	}

	i := strings.Index(conf, "server_name custom.example.com;\n\n")
	if i < 0 {
		t.Fatalf("extra host must get a serving 443 block (server_name followed by a blank line, not folded into the 80 redirect):\n%s", conf)
	}
	block := conf[i:]
	if end := strings.Index(block, "\n}\n"); end >= 0 {
		block = block[:end]
	}
	if strings.Contains(block, "return 301") {
		t.Errorf("extra host 443 block must SERVE, not redirect:\n%s", block)
	}
	if !strings.Contains(block, "proxy_pass http://custom-app:9000;") {
		t.Errorf("extra host 443 block must proxy to its own app:port:\n%s", block)
	}
}

// A custom domain whose cert has not been issued yet must NOT get a 443 block:
// nginx refuses to start when ssl_certificate points at a missing file, so an
// eager TLS vhost takes down the whole ingress -- every site on the VM, not just
// the new domain. Until issuance the host is served over plain http.
func TestRenderNginxConf_ExtraHostWithoutCertStaysHTTP(t *testing.T) {
	conf := RenderNginxConf(VMIngressSpec{
		Host:        "fin-data.pro",
		SSLRedirect: true,
		TLS: VMIngressTLS{
			Enabled:  true,
			CertPath: "/etc/nginx/certs/live/fin-data.pro/fullchain.pem",
			KeyPath:  "/etc/nginx/certs/live/fin-data.pro/privkey.pem",
		},
		ACMEWebroot: "/var/www/acme",
		Rules:       []VMIngressRule{{Path: "/", App: "frontend", Port: 5173}},
		ExtraHosts: []VMExtraHost{{
			Host:     "pending.example.com",
			CertPath: "/etc/nginx/certs/live/pending.example.com/fullchain.pem",
			KeyPath:  "/etc/nginx/certs/live/pending.example.com/privkey.pem",
			App:      "custom-app",
			Port:     9000,
		}},
	})
	if strings.Contains(conf, "/etc/nginx/certs/live/pending.example.com/") {
		t.Fatalf("cert-less host must not be referenced by any ssl_certificate:\n%s", conf)
	}
	if !strings.Contains(conf, "proxy_pass http://custom-app:9000;") {
		t.Errorf("cert-less host must still serve over http:\n%s", conf)
	}
	if !strings.Contains(conf, "ssl_certificate /etc/nginx/certs/live/fin-data.pro/fullchain.pem;") {
		t.Errorf("canonical host must keep its TLS vhost:\n%s", conf)
	}
}

// The http-01 challenge must survive the http->https redirect. A server-level
// `return 301` runs in the rewrite phase, before location selection, so the
// redirect has to live in `location /` instead.
func TestRenderNginxConf_ACMEChallengeIsNotRedirected(t *testing.T) {
	conf := RenderNginxConf(VMIngressSpec{
		Host:        "fin-data.pro",
		Aliases:     []string{"www.fin-data.pro"},
		SSLRedirect: true,
		TLS:         VMIngressTLS{Enabled: true, CertPath: "/c.pem", KeyPath: "/k.pem"},
		ACMEWebroot: "/var/www/acme",
		Rules:       []VMIngressRule{{Path: "/", App: "frontend", Port: 5173}},
	})
	challenges := strings.Count(conf, "location /.well-known/acme-challenge/ {")
	if challenges != 2 {
		t.Fatalf("every listen-80 vhost needs the challenge location, got %d:\n%s", challenges, conf)
	}
	for _, block := range strings.Split(conf, "server {") {
		if !strings.Contains(block, "listen 80;") || !strings.Contains(block, "return 301") {
			continue
		}
		redirect := strings.Index(block, "return 301")
		challenge := strings.Index(block, "location /.well-known/acme-challenge/")
		if challenge < 0 || challenge > redirect {
			t.Errorf("redirect must not precede/shadow the challenge location:\n%s", block)
		}
		if !strings.Contains(block, "location / {\n        return 301") {
			t.Errorf("redirect must be scoped to `location /`, not the server block:\n%s", block)
		}
	}
}

func TestIngressServiceBlock_MountsACMEWebroot(t *testing.T) {
	spec := VMIngressSpec{Host: "x", ACMEWebroot: "/var/www/acme", ACMEWebrootSrc: "/var/lib/dada/acme"}
	block := spec.ServiceBlock("", "./nginx.conf", "/etc/letsencrypt", "")
	vols, _ := block["volumes"].([]string)
	var found bool
	for _, v := range vols {
		if v == "/var/lib/dada/acme:/var/www/acme:ro" {
			found = true
		}
	}
	if !found {
		t.Fatalf("acme webroot must be mounted into nginx, got %v", vols)
	}
}

func TestRenderNginxConf_MinVersion13(t *testing.T) {
	conf := RenderNginxConf(VMIngressSpec{Host: "x", TLS: VMIngressTLS{Enabled: true, MinVersion: "1.3"}})
	if !strings.Contains(conf, "ssl_protocols TLSv1.3;") || strings.Contains(conf, "TLSv1.2") {
		t.Errorf("minVersion 1.3 should emit only TLSv1.3:\n%s", conf)
	}
}

func TestRenderPostgresService_ReproducesFindata(t *testing.T) {
	svc, vol, pc := RenderPostgresService(VMDatabaseSpec{
		ServiceName: "postgres",
		Version:     "16",
		Database:    "feedback",
		User:        "postgres",
		Password:    "pswd",
		VolumeName:  "compose_profi_pg_data",
		HostPort:    65433,
		Params:      map[string]string{"max_connections": "100", "shared_buffers": "128MB"},
	})
	for _, w := range []string{
		"image: postgres:16-alpine",
		"POSTGRES_DB: feedback",
		"POSTGRES_USER: postgres",
		"- compose_profi_pg_data:/var/lib/postgresql/data",
		`- "65433:5432"`,
		"max_connections=100",
		"shared_buffers=128MB",
	} {
		if !strings.Contains(svc, w) {
			t.Errorf("postgres service missing %q\n---\n%s", w, svc)
		}
	}
	if vol != "compose_profi_pg_data" {
		t.Errorf("external volume = %q, want compose_profi_pg_data", vol)
	}
	if !strings.Contains(pc, "max_connections = 100") {
		t.Errorf("postgresql.conf missing tunable:\n%s", pc)
	}
}

// No params → no command / no conf (image defaults), still external volume.
func TestRenderPostgresService_Defaults(t *testing.T) {
	svc, _, pc := RenderPostgresService(VMDatabaseSpec{
		ServiceName: "db", Database: "app", User: "app", Password: "x", VolumeName: "app_data",
	})
	if strings.Contains(svc, "command:") || pc != "" {
		t.Errorf("no params should emit no command/conf:\n%s", svc)
	}
	if !strings.Contains(svc, "image: postgres:16-alpine") {
		t.Errorf("default image should be postgres:16-alpine:\n%s", svc)
	}
}

// Integration seam: typed Resource specs → AppServiceSpec.Service → the concurrent
// session's RenderAggregateCompose. Proves the typed provider blocks aggregate
// into ONE compose with the external DB volume pinned.
func TestTypedResources_InAggregateCompose(t *testing.T) {
	db := VMDatabaseSpec{
		ServiceName: "postgres", Version: "16", Database: "feedback",
		User: "postgres", Password: "pswd", VolumeName: "compose_profi_pg_data", HostPort: 65433,
	}
	ing := VMIngressSpec{
		Host: "fin-data.pro", SSLRedirect: true, BasicAuth: "/etc/nginx/.htpasswd",
		TLS:   VMIngressTLS{Enabled: true, MinVersion: "1.2", CertPath: "/etc/nginx/certs/live/fin-data.pro/fullchain.pem"},
		Rules: []VMIngressRule{{Path: "/api/", App: "backend", Port: 8001}, {Path: "/", App: "frontend", Port: 5173}},
	}
	specs := []AppServiceSpec{
		{AppName: "postgres", Service: db.ServiceBlock()},
		{AppName: "nginx", Service: ing.ServiceBlock("nginx:1.27-alpine", "/home/ubuntuuser/compose/nginx/default.conf.template", "/etc/letsencrypt", "/home/ubuntuuser/compose/nginx/.htpasswd")},
		{AppName: "backend", Image: "nexus.dada-tuda.ru/dada/profi-backend:master-1.0.0-194", HasEnv: true},
	}
	volName, volDef := db.ExternalVolumeEntry()
	compose, err := RenderAggregateCompose(specs, map[string]any{volName: volDef})
	if err != nil {
		t.Fatalf("aggregate render: %v", err)
	}
	for _, w := range []string{
		"postgres:", "nginx:", "backend:",
		"POSTGRES_DB: feedback",
		"compose_profi_pg_data",
		"external: true",
		"80:80", "443:443",
		"master-1.0.0-194",
	} {
		if !strings.Contains(compose, w) {
			t.Errorf("aggregate compose missing %q\n---\n%s", w, compose)
		}
	}
}
