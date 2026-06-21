// Package registry talks to Nexus, the single registry after the Harbor
// retirement (ADR-010). In the Jenkins-as-control-plane model the build job
// (jenkins-lib) owns the push to Nexus; the control plane only *reads* Nexus to
// confirm what the job claims it produced. So this surface is read-only:
//   - image-ref string builders (digest-pinned),
//   - a Docker Registry v2 manifest existence check (confirm the pushed image),
//   - a raw-repo HEAD (confirm an APK/AAB exists and its size).
// No project/robot provisioning — Jenkins holds the Nexus push credentials.
package registry

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Registry is the read-only Nexus surface the control plane needs.
type Registry interface {
	// Host returns the bare Docker registry host (no scheme) used in image refs.
	Host() string
	// ImageURI builds the canonical immutable image reference (digest pin).
	ImageURI(projectSlug, appName, digest string) string
	// VerifyImageDigest confirms a manifest exists in the Nexus Docker repo for
	// the given <projectSlug>/<appName> at the given digest (sha256:...).
	VerifyImageDigest(ctx context.Context, projectSlug, appName, digest string) (bool, error)
	// HeadRawArtifact confirms a raw-repo object exists and returns its size.
	HeadRawArtifact(ctx context.Context, rawURL string) (size int64, ok bool, err error)
}

// Nexus implements Registry against a Nexus3 instance.
type Nexus struct {
	host        string // nexus-docker.dada-tuda.ru[:port] (scheme-less, for image refs)
	dockerAPI   string // https://nexus-docker.dada-tuda.ru[:port] (for /v2 calls)
	user, token string
	http        *http.Client
}

// NewNexus returns a Nexus client. dockerHost may carry a scheme; image refs use
// the scheme-less host, the /v2 API uses https.
func NewNexus(dockerHost, user, token string) *Nexus {
	host := strings.TrimRight(dockerHost, "/")
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")

	api := strings.TrimRight(dockerHost, "/")
	if !strings.HasPrefix(api, "http://") && !strings.HasPrefix(api, "https://") {
		api = "https://" + api
	}

	return &Nexus{
		host:      host,
		dockerAPI: api,
		user:      user,
		token:     token,
		http:      &http.Client{Timeout: 30 * time.Second},
	}
}

func (n *Nexus) Host() string { return n.host }

func (n *Nexus) ImageURI(projectSlug, appName, digest string) string {
	d := digest
	if !strings.HasPrefix(d, "sha256:") {
		d = "sha256:" + d
	}
	return fmt.Sprintf("%s/%s/%s@%s", n.host, projectSlug, appName, d)
}

// VerifyImageDigest issues a Docker Registry v2 manifest GET by digest. A 200
// means the image the Jenkins job claims it pushed really exists in Nexus.
func (n *Nexus) VerifyImageDigest(ctx context.Context, projectSlug, appName, digest string) (bool, error) {
	d := digest
	if !strings.HasPrefix(d, "sha256:") {
		d = "sha256:" + d
	}
	repo := projectSlug + "/" + appName
	u := fmt.Sprintf("%s/v2/%s/manifests/%s", n.dockerAPI, repo, d)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	req.SetBasicAuth(n.user, n.token)
	// Accept both schema2 and OCI manifests so the check is content-type agnostic.
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.oci.image.index.v1+json")
	resp, err := n.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("verify image: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("verify image %s@%s: %d", repo, d, resp.StatusCode)
	}
}

// HeadRawArtifact confirms a raw-repo artifact exists and returns its size from
// Content-Length. rawURL is the absolute URL the build emitted in its console
// marker (e.g. https://nexus/repository/raw-hosted/<path>/app.apk).
func (n *Nexus) HeadRawArtifact(ctx context.Context, rawURL string) (int64, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return 0, false, err
	}
	req.SetBasicAuth(n.user, n.token)
	resp, err := n.http.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("head raw artifact: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return 0, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("head raw artifact %s: %d", rawURL, resp.StatusCode)
	}
	return resp.ContentLength, true, nil
}

var _ Registry = (*Nexus)(nil)
