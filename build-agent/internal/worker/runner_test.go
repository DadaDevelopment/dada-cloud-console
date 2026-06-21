package worker

import "testing"

func TestInjectToken(t *testing.T) {
	got := injectToken("https://github.com/acme/app.git", "x-access-token", "tok123")
	want := "https://x-access-token:tok123@github.com/acme/app.git"
	if got != want {
		t.Errorf("injectToken = %q, want %q", got, want)
	}
	// non-https passthrough
	if got := injectToken("git@github.com:acme/app.git", "u", "t"); got != "git@github.com:acme/app.git" {
		t.Errorf("ssh url should be untouched, got %q", got)
	}
}

func TestParseMarkerImage(t *testing.T) {
	var out buildOutcome
	parseMarker("==> image: nexus.dada/proj/app@sha256:abc123", &out)
	if out.imageURI != "nexus.dada/proj/app@sha256:abc123" {
		t.Fatalf("imageURI=%q", out.imageURI)
	}
	if len(out.artifacts) != 0 {
		t.Fatalf("unexpected artifacts %+v", out.artifacts)
	}
}

func TestParseMarkerArtifacts(t *testing.T) {
	var out buildOutcome
	parseMarker("==> artifact: apk https://nexus/raw/app.apk 10485760 deadbeef 42", &out)
	parseMarker("==> artifact: aab https://nexus/raw/app.aab 9000000 cafebabe 42", &out)
	parseMarker("just a normal log line", &out)
	if len(out.artifacts) != 2 {
		t.Fatalf("want 2 artifacts, got %d", len(out.artifacts))
	}
	a := out.artifacts[0]
	if a.typ != "apk" || a.nexusURL != "https://nexus/raw/app.apk" || a.size != 10485760 || a.sha256 != "deadbeef" || a.versionCode != 42 {
		t.Fatalf("bad apk artifact: %+v", a)
	}
	if out.artifacts[1].typ != "aab" {
		t.Fatalf("bad aab artifact: %+v", out.artifacts[1])
	}
}

func TestParseMarkerMalformedArtifactIgnored(t *testing.T) {
	var out buildOutcome
	parseMarker("==> artifact: apk only-three fields", &out)
	if len(out.artifacts) != 0 {
		t.Fatalf("malformed artifact should be ignored, got %+v", out.artifacts)
	}
}

func TestImageDigest(t *testing.T) {
	if d := imageDigest("host/proj/app@sha256:abc"); d != "sha256:abc" {
		t.Errorf("digest=%q", d)
	}
	if d := imageDigest("host/proj/app:tag"); d != "" {
		t.Errorf("want empty for tag ref, got %q", d)
	}
}
