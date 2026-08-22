package renderer

import (
	"strings"
	"testing"
)

const gatewayStyleValues = `# this file is edited by hand; the build job does not know it
common:
  image:
    # Nexus retention GC's mutable :SNAPSHOT tags, so a pinned tag disappears without
    # warning. Surviving tags as of 2026-08-18: develop-0.0.1-SNAPSHOT-27.
    tag: develop-0.0.1-SNAPSHOT-27
  servicePort: 8080 # the startup probe hits this port
  # do not drop useDotEnv: the token only exists in /app/.env
  useDotEnv: true
  extraEnv:
    # ServiceIdentity token (ADR-021): the console mints it, git never holds the value
    - name: AGENTSYNC_CLIENT_SECRET
      valueFrom:
        secretKeyRef:
          name: telemost-bot-identity
          key: client-secret
`

func TestConsoleWriteKeepsTheWarningsHumansLeftInGit(t *testing.T) {
	rendered := "common:\n  image:\n    tag: develop-0.0.1-SNAPSHOT-27\n  servicePort: 8080\n  useDotEnv: true\n" +
		"  extraEnv:\n    - name: AGENTSYNC_CLIENT_SECRET\n      valueFrom:\n        secretKeyRef:\n          name: telemost-bot-identity\n          key: client-secret\n"

	out, err := MergeAppValuesWith(gatewayStyleValues, rendered, MergeOptions{})
	if err != nil {
		t.Fatalf("merging: %v", err)
	}

	for _, want := range []string{
		"Nexus retention GC",
		"Surviving tags as of 2026-08-18",
		"the startup probe hits this port",
		"the token only exists in /app/.env",
		"this file is edited by hand",
		"ADR-021",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the console write deleted the note %q a human left in git\n--- got ---\n%s", want, out)
		}
	}
}
