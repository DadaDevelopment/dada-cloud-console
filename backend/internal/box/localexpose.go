package box

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// LocalExposer publishes one port of a box through a real edge.
//
// It is the broker ingress's stand-in and it makes a real hop: a listener on the
// host, an httputil.ReverseProxy in front of the box's port, and a hostname under
// the platform wildcard. A caller's request is proxied and answered by the process
// running inside the box — so `curl` getting a 200 through it is evidence about
// the box, not about this type.
//
// Two product rules are enforced here rather than documented elsewhere:
//
//   - The hostname is ASSIGNED BY THE PLATFORM under *.box.dada-tuda.ru and is
//     never chosen by the caller. Custom domains are a crystallization feature,
//     which also removes most of the phishing incentive on throwaway bodies.
//   - X-Robots-Tag: noindex on every response. An ephemeral body must not
//     accumulate search-engine presence it will outlive by hours.
//
// What it is NOT: the production ingress. That is an Ingress object plus a
// DNS-01 wildcard certificate replicated to the broker (per-host Let's Encrypt on
// a box would hit the 50-certificates-per-domain-per-week ceiling, which means 50
// boxes a week would end the product). There is no cluster and no certificate
// authority reachable here, so the edge is plain HTTP on loopback and the report
// says so instead of implying TLS.
type LocalExposer struct {
	// HostnameBase is the platform wildcard the assigned hostname lives under.
	HostnameBase string

	mu   sync.Mutex
	live map[string]*exposure
}

type exposure struct {
	hostname string
	port     int
	edgePort int
	srv      *http.Server
}

// Exposure is what the control plane records and returns.
type Exposure struct {
	Hostname string `json:"hostname"`
	Port     int    `json:"port"`
	EdgePort int    `json:"edge_port"`
	URL      string `json:"url"`
	Cert     string `json:"cert"`
}

var _ Exposer = (*LocalExposer)(nil)

// NewLocalExposer builds an exposer publishing under base.
func NewLocalExposer(base string) *LocalExposer {
	if base == "" {
		base = "box.dada-tuda.ru"
	}
	return &LocalExposer{HostnameBase: base, live: map[string]*exposure{}}
}

// Expose publishes port of the box named boxName and returns the assigned
// hostname and the edge URL that answers it.
func (e *LocalExposer) Expose(boxName string, port int) (Exposure, error) {
	if port < 1 || port > 65535 {
		return Exposure{}, fmt.Errorf("box: port %d out of range", port)
	}
	hostname := fmt.Sprintf("%s-%d.%s", strings.ToLower(boxName), port, e.HostnameBase)

	e.mu.Lock()
	if existing, ok := e.live[hostname]; ok {
		e.mu.Unlock()
		return Exposure{
			Hostname: hostname, Port: existing.port, EdgePort: existing.edgePort,
			URL:  fmt.Sprintf("http://127.0.0.1:%d/", existing.edgePort),
			Cert: "none (plain HTTP loopback edge; production uses the DNS-01 wildcard)",
		}, nil
	}
	e.mu.Unlock()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Exposure{}, fmt.Errorf("box: bind edge listener: %w", err)
	}
	edgePort := ln.Addr().(*net.TCPAddr).Port

	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		// 502 with the reason, not a blank 500: the common case is "nothing is
		// listening in the box yet", and an operator needs to see that rather
		// than guess whether the edge or the box is broken.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, "box edge: nothing answered inside the box on port %d: %v\n", port, err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex")
		w.Header().Set("X-Dada-Box-Hostname", hostname)
		proxy.ServeHTTP(w, req)
	})
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()

	e.mu.Lock()
	e.live[hostname] = &exposure{hostname: hostname, port: port, edgePort: edgePort, srv: srv}
	e.mu.Unlock()

	return Exposure{
		Hostname: hostname, Port: port, EdgePort: edgePort,
		URL:  fmt.Sprintf("http://127.0.0.1:%d/", edgePort),
		Cert: "none (plain HTTP loopback edge; production uses the DNS-01 wildcard)",
	}, nil
}

// Unexpose withdraws a published hostname.
func (e *LocalExposer) Unexpose(hostname string) error {
	e.mu.Lock()
	ex, ok := e.live[hostname]
	delete(e.live, hostname)
	e.mu.Unlock()
	if !ok {
		return nil
	}
	return ex.srv.Close()
}
