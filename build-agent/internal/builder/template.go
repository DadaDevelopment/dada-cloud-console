package builder

import _ "embed"

// jobTemplate is the k8s Job manifest run per build. It runs an ephemeral
// rootless buildkitd sidecar (no shared daemon — blast-radius isolation) and a
// builder container that clones, detects (Dockerfile|nixpacks), then
// `buildctl build` with registry-backed cache + --secret mounts for sensitive
// vars, pushing the result to Harbor.
//
// Hardening (plan §4 isolation): gvisor runtimeClass, tainted build node pool,
// automountServiceAccountToken:false, no-perms SA, seccomp, drop-all-caps,
// readOnlyRootFilesystem on builder, runAsNonRoot, resource quotas.
//
//go:embed job.yaml.tmpl
var jobTemplate string

// JobParams are the fields interpolated into jobTemplate.
type JobParams struct {
	BuildID       string
	Namespace     string // ephemeral build-<id> ns
	BuilderImage  string
	RuntimeClass  string
	NodePoolLabel string
	CPULimit      string
	MemLimit      string

	GitURL    string
	GitBranch string
	GitSHA    string
	RootDir   string
	Framework string // "" => entrypoint auto-detects

	ImageName string // primary target ref (sha-digest base), push=true
	ImageTag  string // additional human :<gitsha> tag ref
	CacheRef  string // registry-backed cache tag (one per repo)

	TimeoutSeconds int // activeDeadlineSeconds mirror of context timeout

	// Secret names mounted into the build (created in the ephemeral ns):
	GitSecretName      string // x-access-token / GitLab PAT
	RegistrySecretName string // Harbor robot
	BuildEnvSecretName string // app build env (--secret, not build-arg)
}

// Template returns the embedded Job YAML template for rendering.
func Template() string { return jobTemplate }
