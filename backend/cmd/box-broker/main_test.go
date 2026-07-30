package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests are about the door, not about the HTTP plumbing. The three ways a
// door quietly stops being a door are: it accepts a credential that was withdrawn,
// it accepts one that ran out, and it accepts a request that carries none. Each has
// a test, because each one is invisible from the outside — the box keeps answering,
// and it answers the wrong caller.

func digestFile(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "tokens")
	body := "# <sha256-of-token> <unix-expiry>\n" + strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func line(token string, expiresIn time.Duration) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%s %d", hex.EncodeToString(sum[:]), time.Now().Add(expiresIn).Unix())
}

func TestTokenAcceptedOnlyForALiveDigest(t *testing.T) {
	const good = "dadabox_deadbeef"
	cases := []struct {
		name  string
		lines []string
		token string
		want  bool
	}{
		{"a live session opens the door", []string{line(good, time.Hour)}, good, true},
		// The revoked case is the file NOT containing the digest: the control plane
		// rewrites this file as the whole live set, so withdrawal is an absence.
		{"a withdrawn session is simply absent", []string{line("dadabox_other", time.Hour)}, good, false},
		// The one that cannot be caught by rewriting the file, because nothing calls
		// anything when a session merely runs out.
		{"an expired session is refused by the box's own clock", []string{line(good, -time.Minute)}, good, false},
		{"a line with no expiry is not read as 'never expires'", []string{hashOf(good)}, good, false},
		{"a garbage expiry is refused rather than ignored", []string{hashOf(good) + " soon"}, good, false},
		{"an empty file opens nothing", nil, good, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &broker{cfg: config{TokensFile: digestFile(t, tc.lines...)}}
			got, err := b.tokenAccepted(tc.token)
			if err != nil {
				t.Fatalf("tokenAccepted: %v", err)
			}
			if got != tc.want {
				t.Fatalf("tokenAccepted(%q) = %v, want %v", tc.token, got, tc.want)
			}
		})
	}
}

func hashOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// A missing digest file must be a closed door, not an open one. The file is absent
// in exactly one situation — the broker came up before the control plane wrote it —
// and that is the moment when accepting everyone would be worst.
func TestAMissingDigestFileClosesTheDoor(t *testing.T) {
	b := &broker{cfg: config{TokensFile: filepath.Join(t.TempDir(), "nope")}}
	ok, err := b.tokenAccepted("dadabox_anything")
	if err != nil {
		t.Fatalf("tokenAccepted: %v", err)
	}
	if ok {
		t.Fatal("a broker with no digest file accepted a token")
	}
}

func TestUnauthenticatedRequestsAreRefused(t *testing.T) {
	const good = "dadabox_live"
	b := &broker{cfg: config{TokensFile: digestFile(t, line(good, time.Hour)), BoxName: "box-test"}}
	h := b.authed(b.handleInfo)

	for _, tc := range []struct {
		name   string
		header string
		value  string
		want   int
	}{
		{"no credential", "", "", http.StatusUnauthorized},
		{"a wrong token", "Authorization", "Bearer dadabox_wrong", http.StatusUnauthorized},
		// A console JWT is not a box credential. The door belongs to the box, so
		// something that authenticates against our control plane must not open it.
		{"a bearer that is not a box token", "Authorization", "Bearer eyJhbGciOi.jwt.looking", http.StatusUnauthorized},
		{"the box's own token", "Authorization", "Bearer " + good, http.StatusOK},
		{"the box's own token on the dedicated header", "X-Box-Token", good, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/info", nil)
			if tc.header != "" {
				r.Header.Set(tc.header, tc.value)
			}
			w := httptest.NewRecorder()
			h(w, r)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// The command really runs, and its exit code is reported rather than flattened.
// A door that returned 200 with an empty body for a failing command would be the
// same class of lie as a readiness check that stops at "the process was spawned".
func TestExecRunsTheCommandAndReportsItsExitCode(t *testing.T) {
	b := &broker{cfg: config{TokensFile: digestFile(t), ExecTimeout: 10 * time.Second, BoxName: "box-test"}}

	res, err := b.exec(t.Context(), execRequest{Command: "echo hello-from-the-box"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got := res["exit_code"].(int); got != 0 {
		t.Fatalf("exit_code = %d, want 0", got)
	}
	if !strings.Contains(res["stdout"].(string), "hello-from-the-box") {
		t.Fatalf("stdout = %q, want it to contain the command's output", res["stdout"])
	}

	failed, err := b.exec(t.Context(), execRequest{Command: "exit 7"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got := failed["exit_code"].(int); got != 7 {
		t.Fatalf("exit_code = %d, want 7 — a failing command must not be reported as a working one", got)
	}
}

// tools/call reports a non-zero exit as isError:true rather than as a protocol
// error. Collapsing the two would make a failing build indistinguishable from a
// broken box.
func TestMCPToolCallSeparatesAFailedCommandFromABrokenBox(t *testing.T) {
	const good = "dadabox_live"
	b := &broker{cfg: config{TokensFile: digestFile(t, line(good, time.Hour)), ExecTimeout: 10 * time.Second}}

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_command","arguments":{"command":"exit 3"}}}`
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+good)
	w := httptest.NewRecorder()
	b.authed(b.handleMCP)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Result map[string]any `json:"result"`
		Error  any            `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("a command that exited non-zero was reported as a protocol error: %v", resp.Error)
	}
	if isErr, _ := resp.Result["isError"].(bool); !isErr {
		t.Fatalf("isError = false for a command that exited 3; result = %v", resp.Result)
	}
}

// The box advertises the verb the platform surface deliberately does not (D6). If
// this list ever loses run_command, the box has stopped being the place the
// customer's agent can work, and the only remaining path is through us.
func TestTheBoxAdvertisesTheExecVerbThePlatformDoesNot(t *testing.T) {
	const good = "dadabox_live"
	b := &broker{cfg: config{TokensFile: digestFile(t, line(good, time.Hour))}}
	r := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	r.Header.Set("Authorization", "Bearer "+good)
	w := httptest.NewRecorder()
	b.authed(b.handleMCP)(w, r)

	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range resp.Result.Tools {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"run_command", "box_info"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("the box's MCP surface does not advertise %q; it has %v", want, names)
		}
	}
}

// The broker will not start without a digest file. A broker that started anyway
// would have to either accept everyone or reject everyone, and the first is a hole.
func TestTheBrokerRefusesToStartWithoutADigestFile(t *testing.T) {
	t.Setenv("BOX_BROKER_TOKENS", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("loadConfig accepted an empty BOX_BROKER_TOKENS")
	}
}
