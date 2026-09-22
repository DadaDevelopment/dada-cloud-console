// Command solutionprobe verifies that every image-track card in the catalog
// can actually be installed: its image reference resolves in the registry, a
// linux/amd64 manifest exists, and the port the card declares is what the
// image config exposes. It exists because the catalog is a promise printed on
// an empty screen — a tile that cannot install spends a newcomer's first
// action, and twelve of the first fourteen catalog installs failed exactly
// that way (see experiments E189, backlog 0513).
//
// The check is the same one install-time never does (an image app is deployed
// by reference and verified only by becoming a pod), so rot between "card
// written" and "card clicked" is invisible without a probe. Run it from the
// backend module directory or through automator/state/probe-catalog-images.sh;
// a non-zero exit names every dead card, and a dead card must come off the
// catalog or move to a ref that resolves.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/solutions"
)

type probeResult struct {
	Slug string
	Ref  string
	OK   bool
	Note string
}

var accepts = []string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}

func main() {
	client := &http.Client{Timeout: 30 * time.Second}
	results := make([]probeResult, 0, len(solutions.V1))
	for _, s := range solutions.V1 {
		if !s.IsImage() {
			results = append(results, probeResult{Slug: s.Slug, Ref: "(build track)", OK: true, Note: "not probed; build-track cards cannot be verified from a registry"})
			continue
		}
		results = append(results, probeImage(client, s.Slug, s.Image, s.Port))
	}

	failed := 0
	for _, r := range results {
		status := "OK"
		if !r.OK {
			status = "DEAD"
			failed++
		}
		fmt.Printf("%-16s %-8s %s %s\n", r.Slug, status, r.Ref, r.Note)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Slug < results[j].Slug })
	if failed > 0 {
		fmt.Printf("\n%d of %d cards cannot be installed; take them off the catalog or repin them\n", failed, len(results))
		os.Exit(1)
	}
	fmt.Printf("\nall %d catalog cards resolve\n", len(results))
}

func probeImage(client *http.Client, slug, ref string, port int) probeResult {
	res := probeResult{Slug: slug, Ref: ref}
	name, digest := splitDigest(ref)
	repo, tag := splitTag(name, digest)
	tok, err := registryToken(client, repo)
	if err != nil {
		res.Note = fmt.Sprintf("token: %v", err)
		return res
	}
	man, code, err := getJSON(client, repo, tok, "manifests/"+digestPart(tag, digest), accepts)
	if err != nil {
		res.Note = fmt.Sprintf("manifest: %v", err)
		return res
	}
	if code != http.StatusOK {
		res.Note = fmt.Sprintf("manifest http %d", code)
		return res
	}
	if idx, ok := man["manifests"].([]any); ok {
		d, err := amd64Digest(idx)
		if err != nil {
			res.Note = err.Error()
			return res
		}
		man, code, err = getJSON(client, repo, tok, "manifests/"+d, accepts)
		if err != nil {
			res.Note = fmt.Sprintf("amd64 manifest: %v", err)
			return res
		}
		if code != http.StatusOK {
			res.Note = fmt.Sprintf("amd64 manifest http %d", code)
			return res
		}
	}
	cfg, ok := man["config"].(map[string]any)
	if !ok {
		res.Note = "manifest carries no config"
		return res
	}
	cfgDigest, _ := cfg["digest"].(string)
	if cfgDigest == "" {
		res.Note = "manifest config has no digest"
		return res
	}
	body, code, err := getJSON(client, repo, tok, "blobs/"+cfgDigest, accepts)
	if err != nil {
		res.Note = fmt.Sprintf("config blob: %v", err)
		return res
	}
	if code != http.StatusOK {
		res.Note = fmt.Sprintf("config blob http %d", code)
		return res
	}
	ports := exposedTCPPorts(body)
	res.Note = fmt.Sprintf("amd64 exposed %v, card port %d", ports, port)
	res.OK = len(ports) == 0 || intIn(port, ports)
	if len(ports) == 0 {
		res.Note += " (image declares no ports; cannot contradict the card)"
	}
	return res
}

func registryToken(client *http.Client, repo string) (string, error) {
	var tokURL string
	switch {
	case strings.HasPrefix(repo, "ghcr.io/"):
		tokURL = "https://ghcr.io/token?scope=repository:" + strings.TrimPrefix(repo, "ghcr.io/") + ":pull"
	case strings.HasPrefix(repo, "docker.io/"):
		tokURL = "https://auth.docker.io/token?service=registry.docker.io&scope=repository:" + strings.TrimPrefix(repo, "docker.io/") + ":pull"
	default:
		tokURL = "https://auth.docker.io/token?service=registry.docker.io&scope=repository:" + repo + ":pull"
	}
	resp, err := client.Get(tokURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Token == "" {
		return "", fmt.Errorf("empty token, http %d", resp.StatusCode)
	}
	return body.Token, nil
}

func registryHost(repo string) string {
	switch {
	case strings.HasPrefix(repo, "ghcr.io/"):
		return "https://ghcr.io/v2/" + strings.TrimPrefix(repo, "ghcr.io/")
	case strings.HasPrefix(repo, "docker.io/"):
		return "https://registry-1.docker.io/v2/" + strings.TrimPrefix(repo, "docker.io/")
	default:
		return "https://registry-1.docker.io/v2/" + repo
	}
}

func getJSON(client *http.Client, repo, tok, path string, accepts []string) (map[string]any, int, error) {
	req, err := http.NewRequest(http.MethodGet, registryHost(repo)+"/"+path, nil)
	if err != nil {
		return nil, 0, err
	}
	for _, a := range accepts {
		req.Header.Add("Accept", a)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var body map[string]any
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return nil, resp.StatusCode, err
		}
	}
	return body, resp.StatusCode, nil
}

func amd64Digest(manifests []any) (string, error) {
	for _, m := range manifests {
		entry, ok := m.(map[string]any)
		if !ok {
			continue
		}
		platform, _ := entry["platform"].(map[string]any)
		if platform == nil {
			continue
		}
		if platform["os"] == "linux" && platform["architecture"] == "amd64" {
			d, _ := entry["digest"].(string)
			if d != "" {
				return d, nil
			}
		}
	}
	return "", fmt.Errorf("no linux/amd64 manifest in the index")
}

func exposedTCPPorts(config map[string]any) []int {
	cfg, ok := config["config"].(map[string]any)
	if !ok {
		return nil
	}
	portsRaw, ok := cfg["ExposedPorts"].(map[string]any)
	if !ok {
		return nil
	}
	ports := make([]int, 0, len(portsRaw))
	for name := range portsRaw {
		var n int
		if _, err := fmt.Sscanf(name, "%d/tcp", &n); err == nil {
			ports = append(ports, n)
		}
	}
	sort.Ints(ports)
	return ports
}

func intIn(want int, list []int) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func splitDigest(ref string) (name, digest string) {
	if at := strings.LastIndex(ref, "@"); at >= 0 {
		return ref[:at], ref[at+1:]
	}
	return ref, ""
}

func splitTag(name, digest string) (repo, tag string) {
	if digest != "" {
		return name, digest
	}
	if colon := strings.LastIndex(name, ":"); colon >= 0 && !strings.Contains(name[colon:], "/") {
		return name[:colon], name[colon+1:]
	}
	return name, "latest"
}

func digestPart(tagOrDigest, digest string) string {
	if digest != "" {
		return digest
	}
	return tagOrDigest
}
