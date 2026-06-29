// Package detect maps a repo's framework override into the parameter passed to
// the centralized Jenkins job. dada-cloud owns the pipeline (jenkins-lib); user
// repos carry no Jenkinsfile. The control plane keeps the richer framework
// labels for the UI and defaults, then collapses them to the build-family
// parameter the Jenkins job actually understands (Android or web).
package detect

import "strings"

// Framework is the build framework passed to the Jenkins job.
type Framework string

const (
	FrameworkAndroid Framework = "android"
	FrameworkWeb     Framework = "web"
	FrameworkAuto    Framework = "auto" // jenkins-lib auto-detects after clone
)

var webFrameworks = map[string]struct{}{
	"web":           {},
	"auto":          {},
	"nextjs":        {},
	"nuxt":          {},
	"sveltekit":     {},
	"react":         {},
	"nestjs":        {},
	"express":       {},
	"fastify":       {},
	"remix":         {},
	"vite":          {},
	"node":          {},
	"python":        {},
	"fastapi":       {},
	"django":        {},
	"flask":         {},
	"spring":        {},
	"spring-maven":  {},
	"spring-gradle": {},
	"maven":         {},
	"gradle":        {},
	"scala":         {},
	"go":            {},
	"static":        {},
	"dockerfile":    {},
}

var androidFrameworks = map[string]struct{}{
	"android": {},
}

// Resolve maps git_repos.framework_override into the job parameter. Specific
// web/framework labels collapse to the web build family; empty or unrecognized
// values still fall back to auto.
func Resolve(frameworkOverride string) Framework {
	fw := strings.ToLower(strings.TrimSpace(frameworkOverride))
	switch fw {
	case "", string(FrameworkAuto):
		return FrameworkAuto
	case string(FrameworkAndroid):
		return FrameworkAndroid
	case string(FrameworkWeb):
		return FrameworkWeb
	default:
		if _, ok := androidFrameworks[fw]; ok {
			return FrameworkAndroid
		}
		if _, ok := webFrameworks[fw]; ok {
			return FrameworkWeb
		}
		return FrameworkAuto
	}
}
