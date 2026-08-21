package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// missingManifestNpmDetail is the exact text a Node app without a package.json
// must produce, shared by the table case and the real-console case below.
const missingManifestNpmDetail = "the Dockerfile Dada generated for this app runs `npm install`, " +
	"but the repo has no package.json at its root -- add it, or the app's stack was detected wrong " +
	"and needs a Dockerfile of your own"

// TestClassifyFailureRealNpmMissingManifestLog runs the verbatim console of
// dada-build #447 -- the third of three builds tarotreaderhimu@gmail.com
// burned on 2026-08-21 before leaving -- through the classifier.
//
// The fixture exists because the hand-written table case for this same class
// was green while production stayed broken: written from the shape of the
// failure rather than from the log, it omitted npm's two trailing
// continuation lines ("... This is related to npm not being able to find a
// file." and a bare "npm error enoent"). Those two lines are the whole defect
// -- they match causeErrorRe through the prefix alone and sit below the cause,
// so the user was shown the string "npm error enoent" three times. A fixture
// that cannot reproduce the production failure cannot guard against it.
func TestClassifyFailureRealNpmMissingManifestLog(t *testing.T) {
	console, err := os.ReadFile(filepath.Join("testdata", "jenkins-npm-no-package-json.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	code, detail := classifyFailure(string(console))
	if code != buildFailMissingManifest {
		t.Fatalf("code = %q, want %q", code, buildFailMissingManifest)
	}
	if detail != missingManifestNpmDetail {
		t.Fatalf("detail = %q, want %q", detail, missingManifestNpmDetail)
	}
	if strings.Contains(detail, "A complete log of this run") {
		t.Fatalf("detail carries npm's log-file trailer: %q", detail)
	}
}

// TestPickCauseSkipsPrefixOnlyContinuations pins the rule the real log
// exposed: a line that repeats the tool's error prefix and adds nothing is
// punctuation, not a diagnosis.
func TestPickCauseSkipsPrefixOnlyContinuations(t *testing.T) {
	body := []string{
		"npm error code ENOENT",
		"npm error path /app/package.json",
		"npm error enoent Could not read package.json: Error: ENOENT: no such file or directory, open '/app/package.json'",
		"npm error enoent This is related to npm not being able to find a file.",
		"npm error enoent",
	}
	const want = "npm error enoent Could not read package.json: Error: ENOENT: no such file or directory, open '/app/package.json'"
	if got := pickCause(body); got != want {
		t.Fatalf("pickCause = %q, want %q", got, want)
	}
}
