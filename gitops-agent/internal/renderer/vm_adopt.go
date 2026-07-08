package renderer

import (
	"strconv"
	"strings"
)

// Adopt / Desired-State generation (ADR-013 §8.6, design note
// tasks/vm-resource-primitives-design.md). Turns a discovered VM container into a
// TYPED cloud Resource spec — Application, ServiceDatabase, or Ingress — rather
// than a generic "infra" blob. Pure functions; the import flow reviews the result
// (§8.7) before anything is written. Paths/creds it cannot know from discovery
// alone are derived by convention and meant to be confirmed in the wizard.

// ResourceKind is the platform Resource a discovered service maps to.
type ResourceKind string

const (
	KindApplication     ResourceKind = "Application"
	KindServiceDatabase ResourceKind = "ServiceDatabase"
	KindIngress         ResourceKind = "Ingress"
)

// dbImages / proxyImages map a well-known image basename to its Resource kind.
// Anything else is an Application.
var dbImages = map[string]string{
	"postgres": "postgres", "postgresql": "postgres",
	"mysql": "mysql", "mariadb": "mysql",
	"mongo": "mongo", "mongodb": "mongo",
	"redis": "redis", "valkey": "redis", "memcached": "memcached",
	"clickhouse": "clickhouse",
}
var proxyImages = map[string]bool{
	"nginx": true, "traefik": true, "haproxy": true, "caddy": true, "envoy": true,
}

// DiscoveredService is the subset of a DiscoverWorkload container the adopt logic
// needs. Env is the container's environment (from inspect); Volumes are the raw
// "<source>:<dest>" mount strings; Ports are "<host>:<container>/<proto>".
type DiscoveredService struct {
	Name    string
	Image   string
	Env     map[string]string
	Ports   []string
	Volumes []string
}

func imageBase(image string) string {
	repo := image
	if i := strings.LastIndex(repo, "@"); i >= 0 {
		repo = repo[:i]
	}
	if i := strings.LastIndex(repo, ":"); i >= 0 {
		repo = repo[:i]
	}
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		repo = repo[i+1:]
	}
	return strings.ToLower(repo)
}

// ClassifyResource decides which Resource kind a discovered image maps to.
func ClassifyResource(image string) ResourceKind {
	base := imageBase(image)
	if _, ok := dbImages[base]; ok {
		return KindServiceDatabase
	}
	if proxyImages[base] {
		return KindIngress
	}
	return KindApplication
}

// firstHostPort parses the first "<host>:<container>[/proto]" mapping and returns
// the host port (0 if none/unparseable).
func firstHostPort(ports []string) int {
	for _, p := range ports {
		p = strings.SplitN(p, "/", 2)[0]
		parts := strings.SplitN(p, ":", 2)
		if len(parts) == 2 {
			if n, err := strconv.Atoi(parts[0]); err == nil {
				return n
			}
		}
	}
	return 0
}

// namedVolumeFor returns the external named volume mounted at destPrefix (e.g.
// "/var/lib/postgresql/data"), skipping bind mounts. Empty if none.
func namedVolumeFor(volumes []string, destPrefix string) string {
	for _, v := range volumes {
		parts := strings.SplitN(v, ":", 3)
		if len(parts) < 2 {
			continue
		}
		src, dst := parts[0], parts[1]
		if src == "" || isBindMountSource(src) {
			continue
		}
		if strings.HasPrefix(dst, destPrefix) {
			return src
		}
	}
	return ""
}

// BuildVMDatabaseSpec derives a ServiceDatabase spec from a discovered postgres
// service: version from the image, DB/user/password from POSTGRES_* env, the data
// volume pinned external (data-safety), and the published host port if any.
func BuildVMDatabaseSpec(d DiscoveredService) VMDatabaseSpec {
	base := imageBase(d.Image)
	version := ""
	if i := strings.LastIndex(d.Image, ":"); i >= 0 {
		tag := d.Image[i+1:]
		version = strings.SplitN(tag, "-", 2)[0] // "16-alpine" -> "16"
	}
	spec := VMDatabaseSpec{
		ServiceName: d.Name,
		Version:     version,
		Database:    d.Env["POSTGRES_DB"],
		User:        d.Env["POSTGRES_USER"],
		Password:    d.Env["POSTGRES_PASSWORD"],
		VolumeName:  namedVolumeFor(d.Volumes, "/var/lib/postgresql/data"),
		HostPort:    firstHostPort(d.Ports),
	}
	if spec.User == "" {
		spec.User = "postgres"
	}
	// keep the exact discovered image so the tag isn't guessed
	if base == "postgres" || base == "postgresql" {
		spec.Image = d.Image
	}
	return spec
}

// BuildVMIngressSpec derives an Ingress spec from a discovered nginx service's env
// (DOMAIN, *_UPSTREAM, NGINX_SSL_*). Routing PATHS are not in discovery (they live
// in the nginx conf), so they are derived by the platform convention
// backend→/api/, frontend→/ and MUST be confirmed in the import wizard. Returns
// the spec and a list of assumptions the user should review.
func BuildVMIngressSpec(d DiscoveredService) (VMIngressSpec, []string) {
	var notes []string
	host := d.Env["DOMAIN"]
	spec := VMIngressSpec{Host: host, SSLRedirect: true}
	if host != "" {
		spec.Aliases = []string{"www." + host}
	}
	if cert := d.Env["NGINX_SSL_CERT_PATH"]; cert != "" {
		spec.TLS = VMIngressTLS{
			Enabled:    true,
			MinVersion: "1.2",
			CertPath:   cert,
			KeyPath:    d.Env["NGINX_SSL_KEY_PATH"],
		}
	}

	// Upstreams → routing rules by convention. "<app>:<port>".
	addRule := func(path, upstream string) {
		app, port := upstream, 0
		if i := strings.LastIndex(upstream, ":"); i >= 0 {
			app = upstream[:i]
			port, _ = strconv.Atoi(upstream[i+1:])
		}
		spec.Rules = append(spec.Rules, VMIngressRule{Path: path, App: app, Port: port})
	}
	if be := d.Env["BACKEND_UPSTREAM"]; be != "" {
		addRule("/api/", be)
	}
	if fe := d.Env["FRONTEND_UPSTREAM"]; fe != "" {
		addRule("/", fe)
	}
	if len(spec.Rules) > 0 {
		notes = append(notes, "routing paths derived by convention (backend→/api/, frontend→/) — confirm before import")
	} else {
		notes = append(notes, "no *_UPSTREAM env found — routing rules must be entered manually (they live in the nginx conf, not discoverable)")
	}
	notes = append(notes, "basic-auth (if any) is in the nginx conf, not env — set it in the wizard")
	return spec, notes
}
