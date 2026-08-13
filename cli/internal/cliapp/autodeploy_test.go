package cliapp

import (
	"strings"
	"testing"

	"github.com/dada-tuda/console/cli/internal/apiclient"
)

// TestAutoDeployNoteMatchesWhatThePlatformWillActuallyDo keeps the promise
// honest: the webhook build only happens for a repo the GitHub App can deliver
// pushes for, so an anonymously linked repo must not be told "push and it
// deploys itself".
func TestAutoDeployNoteMatchesWhatThePlatformWillActuallyDo(t *testing.T) {
	installed := autoDeployNote(apiclient.GitRepo{AutoDeploy: true, PlatformAccess: "installation"}, "main")
	if !strings.Contains(installed, "соберётся сам") {
		t.Fatalf("installed repo should promise auto deploy: %q", installed)
	}

	anon := autoDeployNote(apiclient.GitRepo{AutoDeploy: true, PlatformAccess: "anonymous"}, "main")
	if strings.Contains(anon, "соберётся сам") {
		t.Fatalf("anonymous repo gets no webhook, must not promise one: %q", anon)
	}
	if !strings.Contains(anon, "GitHub App") {
		t.Fatalf("anonymous repo must name what is missing: %q", anon)
	}

	off := autoDeployNote(apiclient.GitRepo{}, "main")
	if !strings.Contains(off, "выключен") {
		t.Fatalf("auto_deploy=false must say so: %q", off)
	}
}
