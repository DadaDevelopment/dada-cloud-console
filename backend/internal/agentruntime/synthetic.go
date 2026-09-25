package agentruntime

import (
	"os"
	"path"
	"strings"
)

// syntheticUsernamesEnv lists the actor username globs of test traffic: eval
// runs, QA sessions and rollout probes. The agent mutes their traces with the
// same default list (kagent-app/_dada.py, DADA_TRACE_MUTE_USERNAMES); the
// runtime keeps them out of the client's CRM, where every eval run used to
// create one person per scenario.
const syntheticUsernamesEnv = "AGENT_SYNTHETIC_USERNAMES"

const syntheticUsernamesDefault = "qa_*,*_probe,eval_*"

// syntheticActor reports whether the username belongs to test traffic.
func syntheticActor(username string) bool {
	name := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(username), "@"))
	if name == "" {
		return false
	}
	raw, ok := os.LookupEnv(syntheticUsernamesEnv)
	if !ok {
		raw = syntheticUsernamesDefault
	}
	for _, pattern := range strings.Split(raw, ",") {
		if pattern = strings.TrimSpace(pattern); pattern == "" {
			continue
		}
		if matched, _ := path.Match(pattern, name); matched {
			return true
		}
	}
	return false
}
