package api

import (
	"context"
	"net/url"
	"strings"

	"github.com/dada-tuda/console/backend/internal/models"
)

// Reachability of a box's coordinates, from the caller's side of the wire.
//
// A box's SSH host and MCP URL are the POD's address (ADR-019). Inside the
// cluster that is the whole truth; from a laptop it is a black hole. Handing an
// outsider `ssh root@10.244.0.14` and an mcpServers block on the same address
// looks like a working product and behaves like a hang, which is the worst of
// both. So every connection block states which side of the wire its coordinates
// answer on, and points at the one call that changes the answer.
//
// The MCP port is publishable: ClusterExposer puts it on the platform wildcard
// behind the public ingress. SSH is not — the edge is an L7 HTTP proxy, so no
// hostname it assigns can carry an SSH stream.
type boxReach struct {
	ssh          map[string]any
	mcp          map[string]any
	publicMCPURL string
}

// boxReachability resolves whether the box's own MCP endpoint has already been
// published, and renders the honest verdict for both coordinates.
func (h *Handler) boxReachability(ctx context.Context, b models.Box, mcpURL string) boxReach {
	out := boxReach{
		ssh: map[string]any{
			"scope": "cluster",
			"hint": "this address is the box's Pod IP: it answers from inside the cluster only. " +
				"The public edge is an HTTP proxy, so SSH cannot be published on a platform hostname — " +
				"from outside, drive the box through its MCP endpoint instead.",
		},
		mcp: map[string]any{"scope": "cluster"},
	}

	port := brokerPortOf(mcpURL)
	if port == 0 {
		out.mcp["hint"] = "the endpoint carries no port to publish."
		return out
	}

	var published string
	err := h.pool.QueryRow(ctx,
		`SELECT url FROM box_exposures
		   WHERE box_id = $1 AND port = $2 AND withdrawn_at IS NULL
		   ORDER BY created_at DESC LIMIT 1`, b.ID, port).Scan(&published)
	if err != nil || published == "" {
		out.mcp["hint"] = "this address is the box's Pod IP: it answers from inside the cluster only. " +
			"Publish it on a platform hostname with POST /projects/{projectId}/boxes/{boxName}/expose " +
			"and the snippet will carry the public URL."
		return out
	}

	out.publicMCPURL = strings.TrimRight(published, "/") + brokerPathOf(mcpURL)
	out.mcp["scope"] = "public"
	out.mcp["hint"] = "published on the platform wildcard: this URL answers from the internet, " +
		"and the box's own token is what guards it."
	return out
}

// brokerPortOf extracts the port the box's broker serves on, so the exposure
// lookup matches the port that was actually published rather than a guess.
func brokerPortOf(raw string) int {
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	p := u.Port()
	if p == "" {
		return 0
	}
	n := 0
	for _, r := range p {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// brokerPathOf keeps the endpoint's path when the host is swapped for the
// published one, because the broker answers on /mcp and not on the root.
func brokerPathOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return "/mcp"
	}
	return u.Path
}
