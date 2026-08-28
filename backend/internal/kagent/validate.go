package kagent

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxPromptBytes caps a system prompt. The prompt travels as a ConfigMap key,
// and a ConfigMap is capped at 1 MiB for ALL of its keys together -- hitting
// that ceiling fails the apply, not the save, so the refusal would arrive from
// Argo minutes later with no connection to the edit that caused it.
const MaxPromptBytes = 128 * 1024

// MaxNameLength keeps room under the 63-character DNS label limit for the
// suffixes the composition appends: the prompt ConfigMap is "<name>-prompt"
// and the Deployment kagent creates adds a pod-template hash.
const MaxNameLength = 45

// ValidateName reports why name cannot be an agent name, or nil.
//
// The name becomes a Kubernetes object name, a Service hostname and a label
// value, so it is a DNS-1123 label and nothing else. Validating it here rather
// than at apply time is the difference between a field error in the form and a
// claim that sits Unready in the cluster with a message nobody is watching.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("agent name is required")
	}
	if len(name) > MaxNameLength {
		return fmt.Errorf("agent name must be at most %d characters", MaxNameLength)
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i != 0 && i != len(name)-1:
		default:
			return fmt.Errorf("agent name may contain only lowercase letters, digits and dashes, and must start and end with a letter or digit")
		}
	}
	return nil
}

// ValidatePrompt reports why prompt cannot be a system prompt, or nil.
//
// Emptiness is the check that matters: an agent with no prompt still starts,
// still answers, and answers as whatever the base model happens to be. That
// looks like a working agent and is the hardest kind of broken to notice.
func ValidatePrompt(prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("system prompt is required: an agent with an empty prompt starts and answers as the bare model")
	}
	if len(prompt) > MaxPromptBytes {
		return fmt.Errorf("system prompt must be at most %d bytes, got %d", MaxPromptBytes, len(prompt))
	}
	if !utf8.ValidString(prompt) {
		return fmt.Errorf("system prompt must be valid UTF-8")
	}
	return nil
}

// ValidateHeader reports why header cannot be an allowedHeaders entry, or nil.
//
// Header names are matched case-insensitively by the runtime but are written
// lowercase everywhere in the live agents, and a header that does not parse is
// silently not replayed -- the agent then serves every caller as nobody, which
// is a data-leak shape rather than an outage shape.
func ValidateHeader(header string) error {
	if header == "" {
		return fmt.Errorf("header name is required")
	}
	if header != strings.ToLower(header) {
		return fmt.Errorf("header %q must be lowercase", header)
	}
	for _, r := range header {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return fmt.Errorf("header %q may contain only letters, digits, dashes and underscores", header)
		}
	}
	return nil
}

// ProtocolStreamableHTTP and ProtocolSSE are the two MCP transports the runtime
// speaks. The wrong one is not a soft failure: the server never connects, the
// agent starts healthy and answers every question with no tools.
const (
	ProtocolStreamableHTTP = "STREAMABLE_HTTP"
	ProtocolSSE            = "SSE"
)

// ValidateProtocol reports why protocol cannot be an MCP transport, or nil. An
// empty protocol is the default, streamable HTTP.
func ValidateProtocol(protocol string) error {
	switch protocol {
	case "", ProtocolStreamableHTTP, ProtocolSSE:
		return nil
	default:
		return fmt.Errorf("protocol must be %s or %s", ProtocolStreamableHTTP, ProtocolSSE)
	}
}

// ValidateToolURL reports why rawURL cannot be an MCP endpoint, or nil.
//
// Plain HTTP is refused outside the cluster because the whole point of the
// headers on this server is a bearer token: sending it over http hands the
// token to every hop on the way. Inside the cluster the address never leaves
// the node network, and the platform's own tool servers are plain http.
func ValidateToolURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("MCP address is not a URL: %v", err)
	}
	if u.Host == "" {
		return fmt.Errorf("MCP address needs a host, e.g. https://mcp.example.com/mcp")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" || strings.HasSuffix(host, ".svc") || strings.HasSuffix(host, ".svc.cluster.local") {
			return nil
		}
		return fmt.Errorf("plain http is only allowed for a cluster-internal address; use https for %s, or the token in its headers travels in the clear", host)
	default:
		return fmt.Errorf("MCP address must be http or https, got %q", u.Scheme)
	}
}

// ValidateOutgoingHeaderName reports why name cannot be a header this agent
// sends to a tool server, or nil.
//
// Unlike allowedHeaders, which the runtime lowercases when it replays a
// caller's headers, these are written verbatim onto every outgoing call, so
// "Authorization" stays the "Authorization" the third party documents.
func ValidateOutgoingHeaderName(name string) error {
	if name == "" {
		return fmt.Errorf("header name is required")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return fmt.Errorf("header %q may contain only letters, digits, dashes and underscores", name)
		}
	}
	return nil
}

// EnvReferences returns the names a header value refers to as ${VAR}.
func EnvReferences(value string) []string {
	var out []string
	for _, m := range envRefPattern.FindAllStringSubmatch(value, -1) {
		out = append(out, m[1])
	}
	return out
}

var envRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
