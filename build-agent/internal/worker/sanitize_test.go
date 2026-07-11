package worker

import "testing"

func TestSanitizeDropsJenkinsNoise(t *testing.T) {
	drop := []string{
		"[Pipeline] End of Pipeline",
		"[Checks API] No suitable checks publisher found.",
		"// podTemplate",
		"Started by user nexus-for-jenkins",
		"Loading library dada-tuda-jenkins-pipelines@develop",
		" > /usr/bin/git rev-parse",
		"Created Pod: self-managed devops-tools/dada-build",
		"\tContainer [dind] waiting [ContainerCreating] No message",
		"\x1b[8mha:////4DpGRblob==\x1b[0m",
		"   ",
	}
	for _, in := range drop {
		if _, ok := sanitizeLogLine(in); ok {
			t.Errorf("expected drop, kept: %q", in)
		}
	}
}

func TestSanitizeKeepsBuildOutput(t *testing.T) {
	keep := map[string]string{
		"#12 DONE 30.4s": "#12 DONE 30.4s",
		"using repo Dockerfile at src/./Dockerfile":  "using repo Dockerfile at src/./Dockerfile",
		"\x1b[0m#5 [internal] load build definition": "#5 [internal] load build definition",
	}
	for in, want := range keep {
		got, ok := sanitizeLogLine(in)
		if !ok || got != want {
			t.Errorf("in=%q got=%q ok=%v want=%q", in, got, ok, want)
		}
	}
}

func TestSanitizeRedactsSecrets(t *testing.T) {
	in := "+ git clone --depth 1 --branch main https://x-access-token:ghs_FAKEfake0000000000000000000000000000@github.com/ggrk52/magic-mirror.git src"
	got, ok := sanitizeLogLine(in)
	if !ok {
		t.Fatal("clone line dropped")
	}
	if contains(got, "ghs_FAKEfake0000000000000000000000000000") {
		t.Errorf("token leaked: %q", got)
	}
	if !contains(got, "***@github.com") {
		t.Errorf("expected redaction marker, got: %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
