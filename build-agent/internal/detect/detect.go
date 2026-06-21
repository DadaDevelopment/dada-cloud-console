// Package detect maps a repo's framework override into the parameter passed to
// the centralized Jenkins job. dada-cloud owns the pipeline (jenkins-lib); user
// repos carry no Jenkinsfile. The actual auto-detection (Android = gradlew +
// AndroidManifest.xml; else web-container) runs inside the Jenkins job after the
// clone — untrusted source must not enter the control plane — so an unset/unknown
// override resolves to "auto" and the pipeline decides.
package detect

// Framework is the build framework passed to the Jenkins job.
type Framework string

const (
	FrameworkAndroid Framework = "android"
	FrameworkWeb     Framework = "web"
	FrameworkAuto    Framework = "auto" // jenkins-lib auto-detects after clone
)

// Resolve maps git_repos.framework_override into the job parameter. Empty or
// unrecognized → auto.
func Resolve(frameworkOverride string) Framework {
	switch frameworkOverride {
	case string(FrameworkAndroid):
		return FrameworkAndroid
	case string(FrameworkWeb):
		return FrameworkWeb
	default:
		return FrameworkAuto
	}
}
