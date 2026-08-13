package appname

import (
	"regexp"
	"strings"
)

// ProjectPattern is the exact regex the console validates a project slug
// against. Keep this in lockstep with projectSlugRe in
// backend/internal/api/projects.go:24 - it is stricter than an app name: it
// must start with a letter and is 3-40 characters long.
const ProjectPattern = `^[a-z][a-z0-9-]{1,38}[a-z0-9]$`

var projectValidRe = regexp.MustCompile(ProjectPattern)

// NormalizeProject turns an app name into a slug the console will accept as a
// project: it prefixes a letter when the name starts with a digit and pads a
// too-short name, because a folder called "8" or "ui" is ordinary.
func NormalizeProject(s string) string {
	base := Normalize(s)
	if base == "" {
		return ""
	}
	if base[0] >= '0' && base[0] <= '9' {
		base = "p-" + base
	}
	for len(base) < 3 {
		base += "-app"
	}
	if len(base) > 40 {
		base = strings.Trim(base[:40], "-")
	}
	return base
}

// ValidProject reports whether slug already satisfies ProjectPattern.
func ValidProject(slug string) bool {
	return projectValidRe.MatchString(slug)
}
