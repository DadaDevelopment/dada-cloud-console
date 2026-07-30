package box

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// What "ready" means, and why it is this and not something cheaper.
//
// The stop point for time to ready is the exit status of one command executed
// inside the box. The rejected alternatives each hide a real failure:
//
//   - "the API returned" measures our JSON encoder, not a body for the agent.
//     Publishing that number would be the latency equivalent of a dishonest fake
//     door.
//   - "SSH accepted TCP" passes for a listener that accepts and then hangs on key
//     exchange or PAM. Half of the real "fast boot, slow ready" failures live
//     between accept and a usable shell.
//   - "first byte of output" passes for a box that prints a banner and stalls.
//
// The canary also checks the toolchain, not just the channel. The warm image is
// the product: if the agent has to spend four minutes on apt install, the value
// is gone. In a genuinely warm image these version probes cost tens of
// milliseconds, so including them is free — and it makes it structurally
// impossible to ship a box that boots fast and then makes the agent install Node.
//
// Output is key=value lines rather than raw version banners so parsing is exact
// instead of a pile of regexes over vendor-specific formats.

// CanaryCommand is run inside the box; receiving its exit status is T1.
const CanaryCommand = `echo dada-ready` +
	` && echo "node=$(node -v 2>&1)"` +
	` && echo "python=$(python3 -V 2>&1)"` +
	` && echo "go=$(go version 2>&1)"` +
	` && echo "git=$(git --version 2>&1)"` +
	` && echo "docker=$(docker info --format '{{.ServerVersion}}' 2>&1)"`

// readyMarker must appear in the canary's output. It proves we are reading this
// canary's output and not, say, a login banner.
const readyMarker = "dada-ready"

// requiredToolchain is the set of keys the canary must report non-empty. Changing
// this list changes what the platform promises is preinstalled, so it is a
// deliberate edit reviewed alongside the warm image.
var requiredToolchain = []string{"node", "python", "go", "git", "docker"}

// ErrNotReady is the sentinel for a box that is not ready. Callers match on it
// with errors.Is; the wrapped message says which check failed.
var ErrNotReady = errors.New("box: not ready")

// EvaluateReadiness decides whether a canary result means the box is ready.
//
// It is deliberately strict. Under schedule pressure the tempting loosenings are
// "accept a non-zero exit if there is some output" and "drop the toolchain check
// because the image is obviously warm" — both turn the headline latency number
// into a measurement of something the customer does not get.
func EvaluateReadiness(res CanaryResult) error {
	if res.ExitCode != 0 {
		return fmt.Errorf("%w: canary exited %d", ErrNotReady, res.ExitCode)
	}
	if !strings.Contains(res.Stdout, readyMarker) {
		return fmt.Errorf("%w: canary output missing %q marker", ErrNotReady, readyMarker)
	}

	reported := parseCanaryFields(res.Stdout)
	var missing []string
	for _, tool := range requiredToolchain {
		if strings.TrimSpace(reported[tool]) == "" {
			missing = append(missing, tool)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("%w: warm toolchain incomplete, missing or empty: %s",
			ErrNotReady, strings.Join(missing, ", "))
	}
	return nil
}

// parseCanaryFields reads the canary's key=value lines. Lines without an "=" (the
// ready marker, any stray banner) are ignored.
func parseCanaryFields(stdout string) map[string]string {
	fields := make(map[string]string, len(requiredToolchain))
	for _, line := range strings.Split(stdout, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return fields
}
