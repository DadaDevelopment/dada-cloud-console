package api

import (
	"context"
	"os"
	"testing"
)

// TestMain neutralizes every seam in this package that would otherwise reach
// the public internet during a test run.
//
// githubCloneProbe is the only such seam today: linkGitRepo calls it on a
// github connect that carries neither an installation nor a token, and the
// fixtures here link invented repository names. Against a CI worker with real
// egress github.com answers 404 for those names, which is a decisive "not
// clonable" and makes linkGitRepo reject the seed with github_access_required
// -- a verdict that is correct in production and meaningless for a
// port-detection or conflict test. Returning decisive=false keeps the probe out
// of the way for the whole package; tests that want the probe's own
// classification pinned call githubRepoPubliclyClonable directly against an
// httptest server.
func TestMain(m *testing.M) {
	githubCloneProbe = func(context.Context, string) (bool, bool) { return false, false }
	os.Exit(m.Run())
}
