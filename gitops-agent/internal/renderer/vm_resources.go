package renderer

import (
	"fmt"
	"sort"
	"strings"
)

// VM Runtime Providers for first-class cloud Resources (see
// tasks/vm-resource-primitives-design.md). A Resource has a runtime-agnostic spec;
// these functions are the *compose* provider that renders it — the same shape the
// k8s provider produces as an Ingress CR / ServiceDatabaseV2. The AppServer layer
// aggregates the rendered service blocks into one compose. Pure + deterministic so
// they are unit-testable against a VM's real live config.

// ── Network / Ingress ───────────────────────────────────────────────────────

// VMIngressRule routes a path to one Application service:port on the same stack.
type VMIngressRule struct {
	Path string // e.g. "/api/" or "/"
	App  string // target compose service name, e.g. "backend"
	Port int    // target container port, e.g. 8001
}

// VMIngressTLS is the TLS config for an Ingress. CertPath/KeyPath are the in-container
// cert paths (the VM provider mounts the host certs there).
type VMIngressTLS struct {
	Enabled    bool
	MinVersion string // "1.2" | "1.3"
	CertPath   string
	KeyPath    string
}

// VMIngressSpec is the runtime-agnostic routing+TLS Resource. On k8s it renders an
// Ingress CR; here it renders nginx server blocks. It captures exactly what a
// hand-written nginx.conf expressed — nothing runtime-specific leaks in.
type VMIngressSpec struct {
	Host        string     // primary server_name, e.g. "fin-data.pro"
	Aliases     []string   // e.g. ["www.fin-data.pro"] → 301 redirect to Host
	SSLRedirect bool       // listen 80 → 301 https
	TLS         VMIngressTLS // 443 + certs
	BasicAuth   string     // auth_basic_user_file path; empty = no basic auth
	Rules       []VMIngressRule
}

func sslProtocols(minVersion string) string {
	if minVersion == "1.3" {
		return "TLSv1.3"
	}
	return "TLSv1.2 TLSv1.3"
}

const nginxProxyHeaders = `        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Real-IP $remote_addr;`

// RenderNginxConf generates the nginx server blocks for an VMIngressSpec. Replaces
// the hand-authored default.conf.template: www→apex redirect, http→https, and the
// TLS vhost with per-rule proxy locations. The output is what the VM nginx service
// serves; a k8s provider would render the same VMIngressSpec as an Ingress CR.
func RenderNginxConf(spec VMIngressSpec) string {
	var b strings.Builder
	proto := sslProtocols(spec.TLS.MinVersion)

	// Aliases (e.g. www) → 301 to the canonical host, on both 80 and 443.
	for _, alias := range spec.Aliases {
		fmt.Fprintf(&b, "server {\n    listen 80;\n    server_name %s;\n    return 301 https://%s$request_uri;\n}\n\n", alias, spec.Host)
		if spec.TLS.Enabled {
			fmt.Fprintf(&b, "server {\n    listen 443 ssl http2;\n    server_name %s;\n    ssl_certificate %s;\n    ssl_certificate_key %s;\n    ssl_protocols %s;\n    ssl_ciphers HIGH:!aNULL:!MD5;\n    return 301 https://%s$request_uri;\n}\n\n",
				alias, spec.TLS.CertPath, spec.TLS.KeyPath, proto, spec.Host)
		}
	}

	// Canonical host: http → https redirect.
	if spec.SSLRedirect {
		fmt.Fprintf(&b, "server {\n    listen 80;\n    server_name %s;\n    return 301 https://%s$request_uri;\n}\n\n", spec.Host, spec.Host)
	}

	// Canonical host: the TLS vhost with routing.
	fmt.Fprintf(&b, "server {\n    listen 443 ssl http2;\n    server_name %s;\n\n", spec.Host)
	fmt.Fprintf(&b, "    ssl_certificate %s;\n    ssl_certificate_key %s;\n    ssl_protocols %s;\n    ssl_ciphers HIGH:!aNULL:!MD5;\n\n", spec.TLS.CertPath, spec.TLS.KeyPath, proto)
	b.WriteString("    access_log /var/log/nginx/access.log;\n    error_log /var/log/nginx/error.log warn;\n\n")
	if spec.BasicAuth != "" {
		fmt.Fprintf(&b, "    auth_basic \"Private area\";\n    auth_basic_user_file %s;\n\n", spec.BasicAuth)
	}
	for _, r := range spec.Rules {
		fmt.Fprintf(&b, "    location %s {\n        proxy_pass http://%s:%d;\n%s\n    }\n\n", r.Path, r.App, r.Port, nginxProxyHeaders)
	}
	b.WriteString("}\n")
	return b.String()
}

// ── ServiceDatabase (postgres) ──────────────────────────────────────────────

// VMDatabaseSpec is the runtime-agnostic managed-database Resource. On k8s it
// renders a ServiceDatabaseV2 CRD (CNPG); here it renders a postgres compose
// service + external volume + optional postgresql.conf tunables. The platform owns
// the credentials and injects DATABASE_URL into consumers.
type VMDatabaseSpec struct {
	ServiceName string            // compose service name (also the in-stack DSN host)
	Version     string            // e.g. "16"
	Database    string            // POSTGRES_DB
	User        string            // POSTGRES_USER
	Password    string            // POSTGRES_PASSWORD (platform-managed)
	VolumeName  string            // external volume adopted for /var/lib/postgresql/data
	HostPort    int               // optional published port (0 = internal only)
	Params      map[string]string // optional postgresql.conf tunables
	Image       string            // optional image override; default postgres:<Version>-alpine
}

// RenderPostgresService renders the postgres compose service block (as YAML lines
// under `services:`) and, when Params are set, the postgresql.conf content the VM
// provider mounts. The data volume is ALWAYS external — the data-safety invariant
// (adopt the live volume; never mint an empty one). Returns (serviceYAML,
// volumeName, postgresqlConf).
func RenderPostgresService(spec VMDatabaseSpec) (serviceYAML, volumeName, postgresqlConf string) {
	image := spec.Image
	if image == "" {
		v := spec.Version
		if v == "" {
			v = "16"
		}
		image = "postgres:" + v + "-alpine"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "  %s:\n", spec.ServiceName)
	fmt.Fprintf(&b, "    image: %s\n", image)
	b.WriteString("    restart: unless-stopped\n")
	b.WriteString("    environment:\n")
	fmt.Fprintf(&b, "      POSTGRES_DB: %s\n", spec.Database)
	fmt.Fprintf(&b, "      POSTGRES_USER: %s\n", spec.User)
	fmt.Fprintf(&b, "      POSTGRES_PASSWORD: %s\n", spec.Password)
	if spec.HostPort != 0 {
		b.WriteString("    ports:\n")
		fmt.Fprintf(&b, "      - \"%d:5432\"\n", spec.HostPort)
	}
	b.WriteString("    volumes:\n")
	fmt.Fprintf(&b, "      - %s:/var/lib/postgresql/data\n", spec.VolumeName)
	if len(spec.Params) > 0 {
		// Params delivered via -c flags so no config-file mount is needed (edge-safe).
		b.WriteString("    command:\n      - postgres\n")
		keys := make([]string, 0, len(spec.Params))
		for k := range spec.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "      - -c\n      - %s=%s\n", k, spec.Params[k])
		}
		// Also emit an equivalent postgresql.conf for providers that prefer a file.
		var pc strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&pc, "%s = %s\n", k, spec.Params[k])
		}
		postgresqlConf = pc.String()
	}
	return b.String(), spec.VolumeName, postgresqlConf
}
