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
	good := "sha256:44f06c943670ec3d6c01807a089cea597b75deeacae58229df29a45951709c0e"
	if d := imageDigest("host/proj/app@" + good); d != good {
		t.Errorf("digest=%q", d)
	}
	if d := imageDigest("host/proj/app:tag"); d != "" {
		t.Errorf("want empty for tag ref, got %q", d)
	}
	if d := imageDigest("host/proj/app@Name:      host/proj/app:main-b29"); d != "" {
		t.Errorf("want empty for malformed non-digest suffix, got %q", d)
	}
	if d := imageDigest("host/proj/app@sha256:abc"); d != "" {
		t.Errorf("want empty for short digest, got %q", d)
	}
}

func TestParseMarkerWithTimestampPrefix(t *testing.T) {
	var out buildOutcome
	parseMarker("[2026-07-11T20:14:29.539Z] ==> image: nexus.dada-tuda.ru/ggrk52/magic-mirror@sha256:39188ea53df4f218bd54bf12fac66661b3163e057fdbdd5a27f5dad1c087d77b", &out)
	want := "nexus.dada-tuda.ru/ggrk52/magic-mirror@sha256:39188ea53df4f218bd54bf12fac66661b3163e057fdbdd5a27f5dad1c087d77b"
	if out.imageURI != want {
		t.Fatalf("imageURI=%q want %q", out.imageURI, want)
	}
}

func TestParseMarkerIgnoresSetXEcho(t *testing.T) {
	var out buildOutcome
	parseMarker("[2026-07-11T20:14:29.539Z] + echo '==> image: nexus.dada-tuda.ru/ggrk52/magic-mirror@sha256:39188ea53df4f218bd54bf12fac66661b3163e057fdbdd5a27f5dad1c087d77b'", &out)
	if out.imageURI != "" {
		t.Fatalf("set -x echo must not be parsed, got %q", out.imageURI)
	}
}

func TestParseMarkerIgnoresCommitMessage(t *testing.T) {
	var out buildOutcome
	parseMarker(`[2026-07-11T19:56:33.629Z] Commit message: "fix(dada-build): re-emit ==> image: marker dropped"`, &out)
	if out.imageURI != "" {
		t.Fatalf("commit message must not be parsed, got %q", out.imageURI)
	}
}
