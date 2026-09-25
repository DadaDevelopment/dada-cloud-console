package agentruntime

import (
	"fmt"
	"regexp"
	"strings"
)

var replyLinkPattern = regexp.MustCompile(`(?i)(?:https?:/{2}[^\s<>"'«»()]+|\b[a-z0-9-]+(?:\.[a-z0-9-]+)*\.[a-z]{2,}/[^\s<>"'«»()]*)`)

var replyLinkTrailing = ".,;:!?»"

// ParseLinkAllowlist reads a comma-separated list of hosts or host/path
// prefixes: "direct-fxpro.com" admits that host and its subdomains,
// "docs.google.com/spreadsheets" admits only links under that path.
func ParseLinkAllowlist(raw string) []string {
	var out []string
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		entry = strings.TrimLeft(strings.TrimPrefix(strings.TrimPrefix(entry, "https:"), "http:"), "/")
		if entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

// linkDenylist is never sent to a client under AGENT_RUNTIME_HANDOFF_TRIGGERS,
// whatever the allowlist env says (design 2026-09-24 §3): the robinhoodmetals
// channel is not ours to promote, and the allowlist value lives in an
// external gitops repo this code cannot vouch for. Same entry syntax as the
// allowlist; checked before it.
var linkDenylist = []string{"t.me/robinhoodmetals"}

// Reason prefixes of linkLeakReason.
const (
	linkReasonOutside = "link outside allowlist "
	linkReasonDenied  = "denied link "
)

// linkLeakReason names the first link in reply that is denylisted (only when
// deny is set) or whose host is outside the allowlist. An empty allowlist
// disables the allowlist check.
func linkLeakReason(reply string, allowed []string, deny bool) string {
	for _, raw := range replyLinkPattern.FindAllString(reply, -1) {
		link := strings.TrimRight(raw, replyLinkTrailing)
		if strings.Contains(link, "@") {
			continue
		}
		if deny && linkAllowed(link, linkDenylist) {
			return linkReasonDenied + link
		}
		if len(allowed) > 0 && !linkAllowed(link, allowed) {
			return linkReasonOutside + link
		}
	}
	return ""
}

func linkAllowed(link string, allowed []string) bool {
	bare := strings.ToLower(link)
	bare = strings.TrimLeft(strings.TrimPrefix(strings.TrimPrefix(bare, "https:"), "http:"), "/")
	host, path, _ := strings.Cut(bare, "/")
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	host, _, _ = strings.Cut(host, ":")
	for _, entry := range allowed {
		entryHost, entryPath, hasPath := strings.Cut(entry, "/")
		if host != entryHost && !strings.HasSuffix(host, "."+entryHost) {
			continue
		}
		if !hasPath || strings.HasPrefix(path, entryPath) {
			return true
		}
	}
	return false
}

const leakLinkHint = "Предыдущий черновик клиенту не отправлен: в нём была ссылка %s, которой нет в базе знаний. Ссылки бери только из kb_search: партнёрская ссылка в статье ref_link, статистика группы в group_stats; если нужной ссылки в KB нет, ответь без ссылки. Напиши только сам ответ клиенту: 1-3 коротких предложения по-русски."

func leakLinkRepairMessage(reason string) string {
	link := strings.TrimPrefix(strings.TrimPrefix(reason, linkReasonOutside), linkReasonDenied)
	return fmt.Sprintf(leakLinkHint, link)
}
