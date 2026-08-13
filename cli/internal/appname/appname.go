// Package appname normalizes a directory name into the DNS-1123 label the
// console upload API requires for an app name, matching the server-side
// pattern in backend/internal/api/uploadsource.go (uploadAppNameRe).
package appname

import (
	"fmt"
	"regexp"
	"strings"
)

// Pattern is the exact regex the console's upload endpoint validates the app
// name against. Keep this in lockstep with uploadAppNameRe in
// backend/internal/api/uploadsource.go.
const Pattern = `^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`

var (
	validRe    = regexp.MustCompile(Pattern)
	nonAlnumRe = regexp.MustCompile(`[^a-z0-9]+`)
	dashRunRe  = regexp.MustCompile(`-{2,}`)
)

// Normalize lowercases s, replaces every run of non-alphanumeric characters
// with a single hyphen, trims leading/trailing hyphens, and truncates to 63
// characters, producing a name that satisfies Pattern whenever the input
// contains at least one letter or digit.
func Normalize(s string) string {
	lower := strings.ToLower(s)
	replaced := nonAlnumRe.ReplaceAllString(lower, "-")
	collapsed := dashRunRe.ReplaceAllString(replaced, "-")
	trimmed := strings.Trim(collapsed, "-")
	if len(trimmed) > 63 {
		trimmed = strings.Trim(trimmed[:63], "-")
	}
	return trimmed
}

// Validate reports whether name already satisfies Pattern, returning a
// human-readable error naming the exact rule when it does not.
func Validate(name string) error {
	if !validRe.MatchString(name) {
		return fmt.Errorf("app name %q must be lowercase letters, digits and hyphens, "+
			"start and end with a letter or digit, 1-63 characters", name)
	}
	return nil
}
