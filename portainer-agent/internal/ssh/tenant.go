package ssh

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// tenantIDPattern guards the tenant value that gets embedded into a remote shell
// script. Project IDs are UUIDs; anything outside this alphabet is refused rather
// than quoted, so a malformed value can never turn into remote shell syntax.
var tenantIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// TenantScript renders the idempotent remote script that puts PROM_TENANT into
// /etc/dada/vm.env and makes the fleet prometheus-agent pick it up.
//
// The fleet edge stack already sends "X-Scope-OrgID: ${PROM_TENANT}" on
// remote_write; without the variable the header is empty and multitenant Mimir
// answers 401, so every VM sample is dropped. The agent's entrypoint sources
// /etc/dada/vm.env on each container start, which is why a plain docker restart
// (not a stack redeploy) is enough to apply the value. Selection is by compose
// label, not container name, because the edge stack project prefix is not stable.
func TenantScript(tenant string) (string, error) {
	if !tenantIDPattern.MatchString(tenant) {
		return "", fmt.Errorf("refusing unsafe tenant id %q", tenant)
	}
	var b strings.Builder
	b.WriteString("set -eu\n")
	b.WriteString("mkdir -p /etc/dada\n")
	b.WriteString("touch /etc/dada/vm.env\n")
	fmt.Fprintf(&b, "if grep -qx 'PROM_TENANT=%s' /etc/dada/vm.env; then\n", tenant)
	b.WriteString("  echo TENANT_ALREADY_SET\n")
	b.WriteString("  exit 0\n")
	b.WriteString("fi\n")
	b.WriteString("sed -i '/^PROM_TENANT=/d' /etc/dada/vm.env\n")
	fmt.Fprintf(&b, "echo 'PROM_TENANT=%s' >> /etc/dada/vm.env\n", tenant)
	b.WriteString("chmod 600 /etc/dada/vm.env\n")
	b.WriteString("docker ps --filter label=com.docker.compose.service=prometheus-agent -q | xargs -r docker restart >/dev/null 2>&1 || true\n")
	b.WriteString("echo TENANT_WRITTEN\n")
	return b.String(), nil
}

// EnsureTenant SSHes into host and applies TenantScript. It is safe to re-run:
// a VM that already carries the right tenant is left untouched and its
// prometheus-agent is not restarted.
func EnsureTenant(ctx context.Context, host, user, privateKeyPEM, tenant string) (string, error) {
	script, err := TenantScript(tenant)
	if err != nil {
		return "", err
	}
	out, err := runScript(ctx, host, user, privateKeyPEM, script)
	return strings.TrimSpace(out), err
}

// runScript opens one SSH session, feeds the script on stdin and waits for it to
// exit, returning the combined output. Unlike RunBootstrap it does not wait for a
// marker line: these scripts are short and their exit code is the verdict.
func runScript(ctx context.Context, host, user, privateKeyPEM, script string) (string, error) {
	signer, err := gossh.ParsePrivateKey([]byte(privateKeyPEM))
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}
	client, err := gossh.Dial("tcp", dialAddr(host), &gossh.ClientConfig{
		User:            user,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec
		Timeout:         10 * time.Second,
	})
	if err != nil {
		return "", fmt.Errorf("ssh connect: %w", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	var out bytes.Buffer
	session.Stdin = strings.NewReader(script)
	session.Stdout = &out
	session.Stderr = &out

	done := make(chan error, 1)
	go func() { done <- session.Run(bootstrapCommand(user)) }()
	select {
	case <-ctx.Done():
		return out.String(), ctx.Err()
	case err := <-done:
		if err != nil {
			return out.String(), fmt.Errorf("remote script: %w (output: %s)", err, strings.TrimSpace(out.String()))
		}
		return out.String(), nil
	}
}
