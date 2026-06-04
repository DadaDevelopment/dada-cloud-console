package ssh

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"fmt"
	"strings"
	"text/template"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

//go:embed bootstrap.sh.tmpl
var bootstrapFS embed.FS

// BootstrapParams holds template variables for bootstrap.sh.tmpl.
type BootstrapParams struct {
	ServerName               string
	EdgeKey                  string
	EdgeID                   string
	PrometheusRemoteWriteURL string
	PrometheusUser           string
	PrometheusPass           string
	ElasticsearchURL         string
	ElasticsearchAPIKey      string
}

// isClusterInternal reports whether a URL points at an in-cluster Kubernetes
// Service address (…svc.cluster.local). Manual-connect / external VMs run
// outside the cluster network and cannot resolve cluster DNS, so observability
// sidecars pointed at such hosts crash-loop. Treating these as "unconfigured"
// makes the template skip the sidecar — same root-cause class as the
// edge-endpoint in-cluster-URL bug fixed in commit c39d3b7.
func isClusterInternal(rawURL string) bool {
	return strings.Contains(rawURL, ".svc.cluster.local") ||
		strings.Contains(rawURL, ".svc:") ||
		strings.Contains(rawURL, ".svc/") ||
		strings.HasSuffix(rawURL, ".svc")
}

// RenderBootstrap renders bootstrap.sh.tmpl with the given params.
//
// Observability endpoint URLs that resolve only inside the cluster are blanked
// before rendering; an empty endpoint URL makes the template skip that sidecar
// entirely (opt-in observability) rather than deploy it against an unreachable
// target. The Portainer Edge Agent — the critical path to Ready — always renders.
func RenderBootstrap(p BootstrapParams) (string, error) {
	if isClusterInternal(p.PrometheusRemoteWriteURL) {
		p.PrometheusRemoteWriteURL = ""
	}
	if isClusterInternal(p.ElasticsearchURL) {
		p.ElasticsearchURL = ""
	}
	tmplBytes, err := bootstrapFS.ReadFile("bootstrap.sh.tmpl")
	if err != nil {
		return "", fmt.Errorf("read template: %w", err)
	}
	tmpl, err := template.New("bootstrap").Parse(string(tmplBytes))
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return "", fmt.Errorf("render template: %w", err)
	}
	return buf.String(), nil
}

// dialAddr normalizes a host into a "host:port" dial address.
// "ip" → "ip:22"; "ip:port" is passed through unchanged.
func dialAddr(host string) string {
	if strings.Contains(host, ":") {
		return host
	}
	return host + ":22"
}

// RunBootstrap SSHes into host, streams the rendered bootstrap script via stdin,
// and waits for "BOOTSTRAP_COMPLETE" in stdout.
// host: "ip" (defaults to port 22) or "ip:port" for a custom SSH port.
// user: "root" (default on Beget Ubuntu VDS).
// privateKeyPEM: PEM-encoded private key matching the SSH key on the target VM.
func RunBootstrap(ctx context.Context, host, user, privateKeyPEM string, params BootstrapParams) error {
	signer, err := gossh.ParsePrivateKey([]byte(privateKeyPEM))
	if err != nil {
		return fmt.Errorf("parse private key: %w", err)
	}

	sshCfg := &gossh.ClientConfig{
		User:            user,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec — provisioning context only
		Timeout:         10 * time.Second,
	}

	// Retry until SSH port opens (VM may still be booting — up to 5 min).
	addr := dialAddr(host)
	var client *gossh.Client
	for i := 0; i < 30; i++ {
		client, err = gossh.Dial("tcp", addr, sshCfg)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
	if err != nil {
		return fmt.Errorf("ssh connect after retries: %w", err)
	}
	defer client.Close()

	script, err := RenderBootstrap(params)
	if err != nil {
		return err
	}

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	session.Stdin = strings.NewReader(script)

	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	completeCh := make(chan struct{}, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Printf("[bootstrap/%s] %s\n", host, line)
			if strings.Contains(line, "BOOTSTRAP_COMPLETE") {
				completeCh <- struct{}{}
			}
		}
	}()

	if err := session.Start("bash -s"); err != nil {
		return fmt.Errorf("start bash: %w", err)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-completeCh:
		return nil
	}
}
