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
	Host        string       // primary server_name, e.g. "fin-data.pro"
	Aliases     []string     // e.g. ["www.fin-data.pro"] → 301 redirect to Host
	SSLRedirect bool         // listen 80 → 301 https
	TLS         VMIngressTLS // 443 + certs
	BasicAuth   string       // auth_basic_user_file path; empty = no basic auth
	Rules       []VMIngressRule
	ExtraHosts  []VMExtraHost // additional serving vhosts (custom domains), each with its own cert

	// ACMEWebroot is the in-container webroot serving /.well-known/acme-challenge/
	// on port 80 for every vhost; empty disables the challenge locations. The
	// http→https redirect moves into `location /` so the challenge is never
	// redirected away (a server-level `return` runs before location selection).
	ACMEWebroot string
	// ACMEWebrootSrc is the host/stack source bind-mounted at ACMEWebroot.
	ACMEWebrootSrc string
}

// VMExtraHost is an additional serving vhost on the same Ingress, alongside its
// canonical Host — a custom domain routed straight to App:Port with its own
// cert. Unlike Aliases, an ExtraHost SERVES traffic; it never redirects to
// Host.
type VMExtraHost struct {
	Host     string // server_name, e.g. "custom.example.com"
	CertPath string // in-container cert path for this host
	KeyPath  string // in-container key path for this host
	App      string // target compose service name
	Port     int    // target container port
	// TLSReady reports that CertPath/KeyPath exist on the VM. When false the host
	// is served over plain http only: nginx refuses to start against a missing
	// ssl_certificate, so rendering a 443 block ahead of issuance would take the
	// whole ingress — every site on the VM — down.
	TLSReady bool
}

// acmeLocation renders the http-01 challenge location served from webroot, or ""
// when ACME is disabled. Callers put it inside a `listen 80` server block ahead of
// the redirect location.
func acmeLocation(webroot string) string {
	if webroot == "" {
		return ""
	}
	return fmt.Sprintf("    location /.well-known/acme-challenge/ {\n        root %s;\n        default_type \"text/plain\";\n    }\n", webroot)
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
	acme := acmeLocation(spec.ACMEWebroot)
	redirect80 := func(serverName, target string) {
		fmt.Fprintf(&b, "server {\n    listen 80;\n    server_name %s;\n%s", serverName, acme)
		if acme == "" {
			fmt.Fprintf(&b, "    return 301 https://%s$request_uri;\n}\n\n", target)
			return
		}
		fmt.Fprintf(&b, "    location / {\n        return 301 https://%s$request_uri;\n    }\n}\n\n", target)
	}

	// Aliases (e.g. www) → 301 to the canonical host, on both 80 and 443.
	for _, alias := range spec.Aliases {
		redirect80(alias, spec.Host)
		if spec.TLS.Enabled {
			fmt.Fprintf(&b, "server {\n    listen 443 ssl http2;\n    server_name %s;\n    ssl_certificate %s;\n    ssl_certificate_key %s;\n    ssl_protocols %s;\n    ssl_ciphers HIGH:!aNULL:!MD5;\n    return 301 https://%s$request_uri;\n}\n\n",
				alias, spec.TLS.CertPath, spec.TLS.KeyPath, proto, spec.Host)
		}
	}

	// Canonical host: http → https redirect.
	if spec.SSLRedirect {
		redirect80(spec.Host, spec.Host)
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

	for _, h := range spec.ExtraHosts {
		if !h.TLSReady {
			fmt.Fprintf(&b, "\nserver {\n    listen 80;\n    server_name %s;\n%s", h.Host, acme)
			fmt.Fprintf(&b, "    location / {\n        proxy_pass http://%s:%d;\n%s\n    }\n}\n", h.App, h.Port, nginxProxyHeaders)
			continue
		}
		b.WriteString("\n")
		redirect80(h.Host, h.Host)
		fmt.Fprintf(&b, "server {\n    listen 443 ssl http2;\n    server_name %s;\n\n", h.Host)
		fmt.Fprintf(&b, "    ssl_certificate %s;\n    ssl_certificate_key %s;\n    ssl_protocols %s;\n    ssl_ciphers HIGH:!aNULL:!MD5;\n\n", h.CertPath, h.KeyPath, proto)
		fmt.Fprintf(&b, "    location / {\n        proxy_pass http://%s:%d;\n%s\n    }\n}\n", h.App, h.Port, nginxProxyHeaders)
	}
	return b.String()
}

// ServiceBlock renders the nginx Ingress as a compose service block map for the
// AppServer aggregate (fills AppServiceSpec.Service). The generated config content
// (RenderNginxConf) is delivered by the wiring layer to confSrc; certsSrc /
// htpasswdSrc are the host/stack sources for the TLS certs + basic-auth file. Only
// mounts with a non-empty source are emitted.
func (spec VMIngressSpec) ServiceBlock(image, confSrc, certsSrc, htpasswdSrc string) map[string]any {
	if image == "" {
		image = "nginx:1.27-alpine"
	}
	vols := []string{}
	if confSrc != "" {
		vols = append(vols, confSrc+":/etc/nginx/templates/default.conf.template:ro")
	}
	if htpasswdSrc != "" && spec.BasicAuth != "" {
		vols = append(vols, htpasswdSrc+":"+spec.BasicAuth+":ro")
	}
	if certsSrc != "" {
		vols = append(vols, certsSrc+":/etc/nginx/certs:ro")
	}
	if spec.ACMEWebroot != "" && spec.ACMEWebrootSrc != "" {
		vols = append(vols, spec.ACMEWebrootSrc+":"+spec.ACMEWebroot+":ro")
	}
	b := map[string]any{
		"image":   image,
		"restart": "unless-stopped",
		"ports":   []string{"80:80", "443:443"},
	}
	if len(vols) > 0 {
		b["volumes"] = vols
	}
	return b
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

// ServiceBlock renders the postgres ServiceDatabase as a compose service block map
// for the AppServer aggregate (fills AppServiceSpec.Service). The data volume is
// ALWAYS external (data-safety). Params, when set, become `-c key=value` flags so
// no config-file mount is needed.
func (spec VMDatabaseSpec) ServiceBlock() map[string]any {
	image := spec.Image
	if image == "" {
		v := spec.Version
		if v == "" {
			v = "16"
		}
		image = "postgres:" + v + "-alpine"
	}
	b := map[string]any{
		"image":   image,
		"restart": "unless-stopped",
		"environment": map[string]any{
			"POSTGRES_DB":       spec.Database,
			"POSTGRES_USER":     spec.User,
			"POSTGRES_PASSWORD": spec.Password,
		},
		"volumes": []string{spec.VolumeName + ":/var/lib/postgresql/data"},
	}
	if spec.HostPort != 0 {
		b["ports"] = []string{fmt.Sprintf("%d:5432", spec.HostPort)}
	}
	if len(spec.Params) > 0 {
		cmd := []string{"postgres"}
		keys := make([]string, 0, len(spec.Params))
		for k := range spec.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			cmd = append(cmd, "-c", k+"="+spec.Params[k])
		}
		b["command"] = cmd
	}
	return b
}

// ExternalVolumeEntry returns the top-level `volumes:` entry that pins this
// database's data volume external (name + external:true). The AppServer aggregate
// passes these explicitly because it skips volume derivation for verbatim/typed
// service blocks. Empty name → no entry.
func (spec VMDatabaseSpec) ExternalVolumeEntry() (string, map[string]any) {
	if spec.VolumeName == "" {
		return "", nil
	}
	return spec.VolumeName, map[string]any{"external": true, "name": spec.VolumeName}
}
