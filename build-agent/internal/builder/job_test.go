package builder

import (
	"strings"
	"testing"
)

func TestRenderJobValidYAML(t *testing.T) {
	b := NewK8sBuilder(nil)
	job, err := b.render(JobParams{
		BuildID:            "abc123",
		Namespace:          "build-abc123",
		BuilderImage:       "harbor.dada-tuda.ru/infra/builder:latest",
		RuntimeClass:       "gvisor",
		NodePoolLabel:      "dada.io/pool",
		CPULimit:           "2",
		MemLimit:           "4Gi",
		GitURL:             "https://github.com/acme/app.git",
		GitBranch:          "main",
		GitSHA:             "deadbeefdeadbeef",
		RootDir:            ".",
		ImageName:          "harbor.dada-tuda.ru/acme/app:deadbeef",
		ImageTag:           "harbor.dada-tuda.ru/acme/app:latest",
		CacheRef:           "harbor.dada-tuda.ru/acme/app:buildcache",
		TimeoutSeconds:     1200,
		GitSecretName:      "build-git",
		RegistrySecretName: "build-registry",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if job.Name != "build-abc123" {
		t.Errorf("job name = %q", job.Name)
	}
	if job.Namespace != "build-abc123" {
		t.Errorf("job namespace = %q", job.Namespace)
	}
	if got := job.Spec.Template.Spec.RuntimeClassName; got == nil || *got != "gvisor" {
		t.Errorf("runtimeClassName = %v, want gvisor", got)
	}
	if amt := job.Spec.Template.Spec.AutomountServiceAccountToken; amt == nil || *amt {
		t.Errorf("automountServiceAccountToken should be false")
	}
	if len(job.Spec.Template.Spec.Containers) != 2 {
		t.Fatalf("want 2 containers, got %d", len(job.Spec.Template.Spec.Containers))
	}
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != 1200 {
		t.Errorf("activeDeadlineSeconds = %v", job.Spec.ActiveDeadlineSeconds)
	}
}

func TestRenderJobNoOptionalSecrets(t *testing.T) {
	b := NewK8sBuilder(nil)
	job, err := b.render(JobParams{
		BuildID:        "x",
		Namespace:      "build-x",
		BuilderImage:   "img",
		CPULimit:       "1",
		MemLimit:       "1Gi",
		TimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Without NodePoolLabel there must be no tolerations.
	if len(job.Spec.Template.Spec.Tolerations) != 0 {
		t.Errorf("expected no tolerations, got %d", len(job.Spec.Template.Spec.Tolerations))
	}
}

func TestParseDigest(t *testing.T) {
	cases := map[string]string{
		"==> digest:sha256:abc123":               "sha256:abc123",
		"some prefix ==> digest:sha256:deadbeef": "sha256:deadbeef",
		"==> digest:notadigest":                  "",
		"no marker here":                         "",
	}
	for in, want := range cases {
		if got := parseDigest(in); got != want {
			t.Errorf("parseDigest(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTemplateMentionsBuildkit(t *testing.T) {
	if !strings.Contains(Template(), "buildkitd") {
		t.Error("template should reference buildkitd sidecar")
	}
}
