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
		"[2026-07-11T20:14:29.539Z] \x1b[8mha:////4M7blob==\x1b[0m[Pipeline] // container",
		"[2026-07-11T20:14:29.613Z] \x1b[8mha:////blob==\x1b[0m[Pipeline] End of Pipeline",
		"[2026-07-11T20:14:29.637Z] [Checks API] No suitable checks publisher found.",
		"[2026-08-23T22:55:31.446Z]   at PluginClassLoader for workflow-durable-task-step//org.jenkinsci.plugins.workflow.support.steps.ExecutorStepDynamicContext$FilePathTranslator.get(ExecutorStepDynamicContext.java:160)",
		"[2026-08-23T22:55:31.446Z]   at java.base/java.util.concurrent.Executors$RunnableAdapter.call(Unknown Source)",
		"[2026-08-23T22:55:31.446Z]   at hudson.model.ResourceController$1.run(ResourceController.java:100)",
		"Caused by: org.jenkinsci.plugins.workflow.steps.MissingContextVariableException: Required context class",
	}
	for _, in := range drop {
		if _, ok := sanitizeLogLine(in); ok {
			t.Errorf("expected drop, kept: %q", in)
		}
	}
}

func TestSanitizeKeepsUserApplicationStackTrace(t *testing.T) {
	keep := []string{
		"    at Object.<anonymous> (/app/src/index.js:12:7)",
		"\tat com.example.leadgen.Main.main(Main.java:42)",
	}
	for _, in := range keep {
		if _, ok := sanitizeLogLine(in); !ok {
			t.Errorf("expected user stack trace line kept, dropped: %q", in)
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
