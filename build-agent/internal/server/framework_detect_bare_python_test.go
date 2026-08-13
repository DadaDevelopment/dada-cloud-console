package server

import "testing"

// TestBarePythonFolderIsBuildable pins the fix for the deploy a real user hit
// on 2026-08-13: a repo of plain .py modules with no requirements.txt,
// pyproject.toml, setup.py or Dockerfile detected nothing, so Jenkins got an
// empty detected_framework and answered "framework <empty> has no template and repo
// ships no Dockerfile". The pipeline's python branch builds exactly this shape,
// so the only thing missing was the word "python".
func TestBarePythonFolderIsBuildable(t *testing.T) {
	det := detectFakeRepo(t, map[string]string{
		"agent.py":   "print(1)\n",
		"serve.py":   "print(2)\n",
		"service.py": "print(3)\n",
	})
	if det.Framework == nil || *det.Framework != "python" {
		t.Fatalf("framework = %v, want python", det.Framework)
	}
}

// TestBarePythonNeverBeatsARealManifest keeps the fallback last: a helper
// script next to a Node app must not turn that app into a Python build.
func TestBarePythonNeverBeatsARealManifest(t *testing.T) {
	det := detectFakeRepo(t, map[string]string{
		"package.json": pkg(`"next":"14.0.0"`, ""),
		"tools/gen.py": "print(1)\n",
		"scripts.py":   "print(2)\n",
	})
	if det.Framework == nil || *det.Framework == "python" {
		t.Fatalf("framework = %v, want the node framework, not python", det.Framework)
	}
}
