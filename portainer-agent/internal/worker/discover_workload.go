package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dada-tuda/console/portainer-agent/internal/db"
	"github.com/dada-tuda/console/portainer-agent/internal/portainer"
	"github.com/rs/zerolog/log"
)

type discoverWorkloadPayload struct {
	ServerName string `json:"server_name"`
	EndpointID int    `json:"endpoint_id"`
}

// discoveredContainer is the console-facing summary of one running container.
type discoveredContainer struct {
	Name   string            `json:"name"`
	Image  string            `json:"image"`
	State  string            `json:"state"`
	Status string            `json:"status"`
	Ports  []string          `json:"ports"`
	Mounts []discoveredMount `json:"mounts"`
}

type discoveredMount struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}

// discoveryResult is written to operations.validation_result and rendered by the
// console. ExternalVolumesYAML is the data-safety artifact: a ready-to-paste
// compose block pinning every live named volume `external: true` so the gitops
// compose ADOPTS prod data instead of minting an empty `<stack>_<vol>`.
type discoveryResult struct {
	EndpointID          int                   `json:"endpoint_id"`
	Containers          []discoveredContainer `json:"containers"`
	ExternalVolumesYAML string                `json:"external_volumes_yaml"`
	Warnings            []string              `json:"warnings"`
}

// doDiscoverWorkload reads (read-only) the containers/volumes on an enrolled
// endpoint via the Portainer docker proxy and stores an inventory + an
// external-volume compose block on the operation. It NEVER mutates the endpoint.
// This is the cloud-native replacement for the SSH vm-discover.sh script.
func (w *VMWatcher) doDiscoverWorkload(ctx context.Context, op db.Operation) error {
	var p discoverWorkloadPayload
	if err := unmarshalPayload(op.Payload, &p); err != nil {
		return err
	}
	if p.EndpointID == 0 {
		return fmt.Errorf("discover workload: endpoint_id is required (is the VM enrolled?)")
	}

	containers, err := w.portainer.ListContainers(ctx, p.EndpointID, "")
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	result := buildDiscoveryResult(p.EndpointID, containers)
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal discovery: %w", err)
	}
	if err := db.SaveValidationResult(ctx, w.pool, op.ID, resultJSON); err != nil {
		return fmt.Errorf("save discovery result: %w", err)
	}
	log.Info().Str("server", p.ServerName).Int("endpoint", p.EndpointID).
		Int("containers", len(result.Containers)).Msg("workload discovered")
	return db.MarkReady(ctx, w.pool, op.ID)
}

// platformSidecars are the observability/agent containers the platform itself
// injects onto every enrolled VM at bootstrap, keyed by compose SERVICE name (or
// by container name for the standalone edge agent, which runs outside compose).
// They are NOT part of the user's workload, so discovery excludes them —
// otherwise their images and (worse) their named volumes would pollute the adopt
// inventory + external-volume block.
var platformSidecars = map[string]bool{
	"portainer_edge_agent": true,
	"filebeat":             true,
	"fluent-bit":           true,
	"prometheus-agent":     true,
	"container-metrics":    true,
	"cadvisor":             true,
	"node_exporter":        true,
	"node-exporter":        true,
}

// composeProjectLabel and composeServiceLabel are what `docker compose` stamps on
// every container it creates. They are the authoritative identity: a container's
// NAME is "<project>-<service>-<index>", so matching a bare service name against
// the name alone never fires for anything compose-managed.
const (
	composeProjectLabel = "com.docker.compose.project"
	composeServiceLabel = "com.docker.compose.service"
)

// isPlatformSidecar reports whether a discovered container belongs to the
// platform rather than to the user's workload.
//
// The bare-name map alone was not enough and let the whole fleet observability
// stack leak into two user projects on 2026-08-08: the stack is deployed by
// Portainer as compose, so its containers are named
// "edge-vm-observability-fluent-bit-1", never "fluent-bit", and the exact-match
// lookup could not fire. Both users then imported our prometheus-agent,
// fluent-bit, node-exporter and container-metrics as their own apps.
//
// Identity is checked in order of trustworthiness: the compose project (the
// entire fleet stack is platform-owned whatever services it grows next), then
// the compose service, then — for endpoints whose proxy answer carries no labels
// — the same two tokens recovered from the container name.
func isPlatformSidecar(c portainer.Container, name string) bool {
	if project := c.Labels[composeProjectLabel]; isFleetProject(project) {
		return true
	}
	if platformSidecars[c.Labels[composeServiceLabel]] {
		return true
	}
	if platformSidecars[name] {
		return true
	}
	return composeNameLooksPlatform(name)
}

// isFleetProject matches the fleet observability compose project. Portainer
// deploys an edge stack under an "edge-"-prefixed project name on some agent
// versions and under the bare stack name on others, so both spellings count.
func isFleetProject(project string) bool {
	return project == fleetStackName || project == "edge-"+fleetStackName
}

// composeNameLooksPlatform is the label-less fallback: it decides whether a
// container name of the form "<project>-<service>-<index>" belongs to the
// platform. Both the project and the service may themselves contain dashes, so
// the boundary between them is genuinely ambiguous from the name alone — every
// split point is therefore tried, and the name is platform-owned if ANY reading
// yields a known fleet project or a known sidecar service.
//
// Trying every split is safe because both sides are matched against fixed
// platform vocabularies, never against user input: a user container is excluded
// only if some substring of its name is exactly a platform service, which is the
// same collision the bare-name map already accepted.
func composeNameLooksPlatform(name string) bool {
	idx := strings.LastIndex(name, "-")
	if idx <= 0 {
		return false
	}
	if _, err := strconv.Atoi(name[idx+1:]); err != nil {
		return false
	}
	stem := name[:idx]
	for i, ch := range stem {
		if ch != '-' {
			continue
		}
		if isFleetProject(stem[:i]) || platformSidecars[stem[i+1:]] {
			return true
		}
	}
	return false
}

// buildDiscoveryResult turns raw proxy containers into the console summary +
// external-volume block. Pure function (no I/O) so it is unit-testable.
func buildDiscoveryResult(endpointID int, containers []portainer.Container) discoveryResult {
	res := discoveryResult{EndpointID: endpointID, Containers: []discoveredContainer{}, Warnings: []string{}}

	// namedVols maps a live named-volume name → a mount destination (for the note).
	namedVols := map[string]string{}
	skipped := 0

	for _, c := range containers {
		name := strings.TrimPrefix(firstName(c.Names), "/")
		if isPlatformSidecar(c, name) {
			skipped++
			continue
		}
		dc := discoveredContainer{
			Name:   name,
			Image:  c.Image,
			State:  c.State,
			Status: c.Status,
			Ports:  []string{},
			Mounts: []discoveredMount{},
		}
		for _, pt := range c.Ports {
			if pt.PublicPort != 0 {
				dc.Ports = append(dc.Ports, fmt.Sprintf("%d:%d/%s", pt.PublicPort, pt.PrivatePort, pt.Type))
			}
		}
		for _, m := range c.Mounts {
			dc.Mounts = append(dc.Mounts, discoveredMount{
				Type: m.Type, Name: m.Name, Source: m.Source, Destination: m.Destination, RW: m.RW,
			})
			switch m.Type {
			case "volume":
				if m.Name != "" {
					namedVols[m.Name] = m.Destination
				}
			case "bind":
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"container %q bind-mounts host path %q → %q — mirror this host path verbatim in the gitops compose (not a named volume)",
					name, m.Source, m.Destination))
			}
		}
		res.Containers = append(res.Containers, dc)
	}

	if skipped > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"excluded %d platform-managed sidecar container(s) (edge agent and the %s stack: prometheus-agent, fluent-bit, node-exporter, container-metrics, cadvisor) — they are not part of the workload to adopt",
			skipped, fleetStackName))
	}
	res.ExternalVolumesYAML = renderExternalVolumesYAML(namedVols)
	return res
}

// renderExternalVolumesYAML emits the top-level compose `volumes:` block pinning
// every named volume external to its literal live name. Mirrors
// scripts/vm-discover.sh so the SSH and cloud paths produce identical artifacts.
func renderExternalVolumesYAML(namedVols map[string]string) string {
	var b strings.Builder
	b.WriteString("# Auto-generated from live volume names (Portainer docker proxy).\n")
	b.WriteString("# Paste under the gitops compose.yaml top-level 'volumes:' key so\n")
	b.WriteString("# 'docker compose up' ATTACHES the existing prod data. DO NOT rename.\n")
	if len(namedVols) == 0 {
		b.WriteString("# (no named volumes found — check bind-mount warnings and mirror host paths.)\n")
		return b.String()
	}
	names := make([]string, 0, len(namedVols))
	for n := range namedVols {
		names = append(names, n)
	}
	sort.Strings(names)
	b.WriteString("volumes:\n")
	for _, n := range names {
		fmt.Fprintf(&b, "  %s:\n    external: true\n    name: %s          # mounted at %s in prod\n", n, n, namedVols[n])
	}
	return b.String()
}

func firstName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}
