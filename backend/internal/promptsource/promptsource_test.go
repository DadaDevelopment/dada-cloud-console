package promptsource

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/agentruntime"
)

const core = "# Roman, exchange support \u00b7 2026-09-16.native.46\n\nYou are Roman.\n"

func TestParseHeader(t *testing.T) {
	title, version, err := ParseHeader("# Roman, exchange support \u00b7 2026-09-16.native.46")
	if err != nil {
		t.Fatal(err)
	}
	if title != "Roman, exchange support" || version != "2026-09-16.native.46" {
		t.Fatalf("got %q %q", title, version)
	}
	for _, bad := range []string{"Roman \u00b7 v1", "# Roman v1", "# Roman \u00b7 "} {
		if _, _, err := ParseHeader(bad); err == nil {
			t.Fatalf("header %q must be refused", bad)
		}
	}
}

func TestBuildAcceptsReferenceLayout(t *testing.T) {
	files := map[string][]byte{
		"core.md":               []byte(core),
		"domains/deposit.md":    []byte("# Deposit\n"),
		"domains/withdrawal.md": []byte("# Withdrawal\n"),
		"experiments/draft.md":  []byte("ignored"),
		"README.md":             []byte("ignored"),
		"domains/notes.txt":     []byte("ignored"),
		"domains/nested/x.md":   []byte("ignored"),
	}
	b, err := Build(files, "abc1234")
	if err != nil {
		t.Fatal(err)
	}
	if b.Version != "2026-09-16.native.46" || b.Title != "Roman, exchange support" {
		t.Fatalf("header not parsed: %+v", b)
	}
	if len(b.Skills) != 2 || b.Skills[0].Name != "deposit" || b.Skills[1].Name != "withdrawal" {
		t.Fatalf("skills: %+v", b.Skills)
	}
	if b.Bytes() != len(core)+len("# Deposit\n")+len("# Withdrawal\n") {
		t.Fatalf("bytes %d", b.Bytes())
	}
}

func TestBuildRefusals(t *testing.T) {
	cases := map[string]map[string][]byte{
		"missing core":   {"domains/a.md": []byte("x")},
		"core not utf8":  {"core.md": append([]byte("# T \u00b7 v1\n"), 0xff, 0xfe)},
		"skill too big":  {"core.md": []byte(core), "domains/big.md": []byte(strings.Repeat("x", agentruntime.MaxSkillContentBytes+1))},
		"skill empty":    {"core.md": []byte(core), "domains/empty.md": {}},
		"skill not utf8": {"core.md": []byte(core), "domains/bin.md": {0xff}},
		"skill bad name": {"core.md": []byte(core), "domains/-bad.md": []byte("x")},
		"no version":     {"core.md": []byte("# Title only\n")},
	}
	for name, files := range cases {
		if _, err := Build(files, "sha"); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := Build(map[string][]byte{}, "sha"); !errors.Is(err, ErrMissingCore) {
		t.Fatalf("expected ErrMissingCore, got %v", err)
	}
	if _, err := Build(map[string][]byte{"core.md": []byte(core), "domains/big.md": []byte(strings.Repeat("x", 9000))}, "sha"); err == nil || !strings.Contains(err.Error(), "9000 bytes") {
		t.Fatalf("size error must name the size: %v", err)
	}
}

func TestFetcherAgainstGitHubShape(t *testing.T) {
	blobs := map[string]string{
		"b1": core,
		"b2": "# Deposit\n",
		"b3": "experiment",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/repos/o/r/commits/main":
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "deadbeefcafe"})
		case r.URL.Path == "/repos/o/r/git/trees/deadbeefcafe":
			_ = json.NewEncoder(w).Encode(map[string]any{"tree": []map[string]any{
				{"path": "agents/roman/core.md", "type": "blob", "sha": "b1"},
				{"path": "agents/roman/domains", "type": "tree", "sha": "t1"},
				{"path": "agents/roman/domains/deposit.md", "type": "blob", "sha": "b2"},
				{"path": "agents/roman/experiments/x.md", "type": "blob", "sha": "b3"},
				{"path": "agents/other/core.md", "type": "blob", "sha": "b3"},
			}})
		case strings.HasPrefix(r.URL.Path, "/repos/o/r/git/blobs/"):
			id := strings.TrimPrefix(r.URL.Path, "/repos/o/r/git/blobs/")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"content":  base64.StdEncoding.EncodeToString([]byte(blobs[id])),
				"encoding": "base64",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	old := APIBase
	APIBase = srv.URL
	defer func() { APIBase = old }()

	f := Fetcher{Token: "tok"}
	sha, err := f.HeadSHA(context.Background(), "o/r", "main")
	if err != nil || sha != "deadbeefcafe" {
		t.Fatalf("head: %v %q", err, sha)
	}
	files, err := f.Fetch(context.Background(), "o/r", sha, "agents/roman")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || string(files["core.md"]) != core || string(files["domains/deposit.md"]) != "# Deposit\n" {
		t.Fatalf("files: %v", files)
	}
	if _, err := f.Fetch(context.Background(), "o/r", sha, "agents/missing"); err == nil {
		t.Fatal("missing directory must fail")
	}
	if _, err := f.HeadSHA(context.Background(), "o/r", "nope"); err == nil {
		t.Fatal("unknown ref must fail")
	}
}
