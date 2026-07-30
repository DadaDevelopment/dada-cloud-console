// Command box-broker is the box's own door. It runs INSIDE the box and it is the
// endpoint the customer's agent talks to.
//
// WHY THIS BINARY EXISTS AT ALL (D6). The product's promise is that the customer's
// agent keeps its brain local: their source, their prompts and their model
// credentials never traverse our control plane. An exec verb on the platform API
// would break that promise by construction — every keystroke of the customer's work
// would route through us — so there is no such MCP tool on the platform surface and
// no keep-list entry that could create one. The verb has to live somewhere, and the
// only honest somewhere is the box itself. This is that somewhere.
//
// internal/api/box_session.go serves the same two verbs on the control plane and
// says in its own comment that its destination is this binary. It stays as a named
// fallback for a box whose broker did not come up, rather than being deleted in the
// same change that introduces its replacement: removing the only working door in
// the commit that adds an untested one is how a walkable path stops being walkable.
//
// WHAT AUTHENTICATES A CALLER: the box's own `dadabox_` credential from `box up`,
// never a console session and never a user JWT. The broker holds only sha256
// digests, in a file the control plane writes into the box, and compares in
// constant time. The file is re-read on every request, so revoking a session is a
// truncate away from taking effect rather than a restart away.
//
// WHERE THE FILE LIVES, and why it is not a detail: /run inside the box is tmpfs
// and is on ADR-019's machine-owned exclusion list, so neither this binary nor the
// digests it reads are part of the box's userland. A crystallized VM therefore
// contains no broker and no box credential — which is correct, because a permanent
// VM is not an ephemeral body an agent claims, and carrying a live token into one
// would extend a box's credential past the box's life.
//
// THE BOUNDARY THIS BINARY DOES NOT DRAW, stated here rather than left to be
// discovered: on LocalRuntime the box has no network namespace. It shares the
// host's, so this listener is reachable on the host's loopback and the isolation
// between two boxes on one host is filesystem and PID, not network. Production runs
// a box as a Pod with its own network (ADR-019); this file does not make that true.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "box-broker: %v\n", err)
		os.Exit(1)
	}
}

// config is the broker's whole configuration. It is read from the environment
// rather than from flags because the process is launched by the runtime's init
// shell, where a mis-quoted flag is a silent misparse and a missing variable is a
// loud one.
type config struct {
	// Addr is what to listen on. ":0" means "ask the kernel", which is the normal
	// case: on a host that runs several boxes the broker cannot know which port is
	// free, and guessing produces a box that came up but cannot be reached.
	Addr string
	// AddrFile is where the ACTUAL listen address is written once the socket is
	// bound. The control plane reads this file back out of the box instead of
	// assuming a port, so "the broker is listening" and "the URL we published" are
	// the same fact rather than two hopes.
	AddrFile string
	// TokensFile holds one `<sha256-hex> <unix-expiry>` line per live session.
	TokensFile string
	BoxName    string
	// ExecTimeout bounds a single command when the caller names none.
	ExecTimeout time.Duration
}

func loadConfig() (config, error) {
	c := config{
		Addr:        envOr("BOX_BROKER_ADDR", "127.0.0.1:0"),
		AddrFile:    os.Getenv("BOX_BROKER_ADDR_FILE"),
		TokensFile:  os.Getenv("BOX_BROKER_TOKENS"),
		BoxName:     envOr("BOX_NAME", "box"),
		ExecTimeout: 60 * time.Second,
	}
	if c.TokensFile == "" {
		// Refusing to start is the point. A broker with no digest file would either
		// have to accept everyone or reject everyone; the first is a hole and the
		// second is a box that looks up and answers nothing.
		return c, errors.New("BOX_BROKER_TOKENS is required: the broker authenticates with the box's own credential and will not start without the digest file")
	}
	return c, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Addr, err)
	}
	if cfg.AddrFile != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.AddrFile), 0o755); err != nil {
			return err
		}
		// Written AFTER the socket is bound, so the file's existence means the
		// broker is actually accepting rather than about to try.
		if err := os.WriteFile(cfg.AddrFile, []byte(ln.Addr().String()+"\n"), 0o644); err != nil {
			return fmt.Errorf("publish listen address: %w", err)
		}
	}
	b := &broker{cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("/info", b.authed(b.handleInfo))
	mux.HandleFunc("/exec", b.authed(b.handleExec))
	mux.HandleFunc("/mcp", b.authed(b.handleMCP))
	// /healthz is the one unauthenticated route: it reports that a process is
	// listening and nothing else. It cannot see the box, run anything, or name the
	// tenant, so it carries no information a caller could not get from the connect
	// itself.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Fprintf(os.Stdout, "box-broker: listening on %s for box %s\n", ln.Addr(), cfg.BoxName)
	return srv.Serve(ln)
}

type broker struct{ cfg config }

// --- authentication ----------------------------------------------------------

// authed wraps a handler with the box's own credential check.
//
// The digest file is re-read per request on purpose. Caching it would make a
// revoked session keep working until the broker restarted, and a credential whose
// revocation is eventually consistent with the box's life is not a revocation.
func (b *broker) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		ok, err := b.tokenAccepted(token)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "the broker cannot read its credential file: " + err.Error()})
			return
		}
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, found := strings.CutPrefix(h, "Bearer "); found {
		return strings.TrimSpace(after)
	}
	return strings.TrimSpace(r.Header.Get("X-Box-Token"))
}

// tokenAccepted compares sha256(token) against every live digest on file in
// constant time. Every line is compared even after a match: returning early on the
// first hit would make the response time depend on the position of the caller's
// digest in the file, which is a side channel over the very thing being kept secret.
//
// EXPIRY IS ENFORCED HERE, in the box, against the box's own clock. The control
// plane rewrites this file when a session is minted or revoked, but a session that
// simply runs out does so without anyone calling anything — so a broker that
// honoured every line on file would keep a lapsed credential working until the next
// push, on the one path the control plane is deliberately not on.
func (b *broker) tokenAccepted(token string) (bool, error) {
	f, err := os.Open(b.cfg.TokensFile)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	sum := sha256.Sum256([]byte(token))
	want := []byte(hex.EncodeToString(sum[:]))
	now := time.Now().Unix()

	matched := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		digest, expiry, ok := strings.Cut(line, " ")
		if !ok {
			// A line without an expiry is not treated as "never expires". A
			// credential the box cannot time out on its own is a standing
			// credential, and reading a malformed line generously is how one
			// appears.
			continue
		}
		until, convErr := strconv.ParseInt(strings.TrimSpace(expiry), 10, 64)
		if convErr != nil || until <= now {
			continue
		}
		matched |= subtle.ConstantTimeCompare([]byte(digest), want)
	}
	if err := sc.Err(); err != nil {
		return false, err
	}
	return matched == 1, nil
}

// --- verbs -------------------------------------------------------------------

func (b *broker) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, b.info())
}

func (b *broker) info() map[string]any {
	host, _ := os.Hostname()
	return map[string]any{
		"box":      b.cfg.BoxName,
		"hostname": host,
		"pid":      os.Getpid(),
		"door":     "this endpoint runs inside the box; the control plane is not on the path of a command",
		"limits":   "LocalRuntime gives this box no network namespace, so this listener is reachable on the host's loopback",
	}
}

type execRequest struct {
	Command        string `json:"command"`
	WorkingDir     string `json:"working_dir"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func (b *broker) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return
	}
	var req execRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "command is required"})
		return
	}
	res, err := b.exec(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// execPrelude sources the box's env before the caller's command so a credential
// injected by an attach is present in the very next command without the caller
// re-reading anything. It is character-for-character the prelude
// internal/box/localruntime.go uses, because a command must not behave differently
// depending on which of the two doors it came through.
const execPrelude = `set -a
[ -r /etc/dada/box.env ] && . /etc/dada/box.env
set +a
`

// exec runs one command inside the box.
//
// The command is handed to the shell on STDIN rather than as an argv element. That
// is not a security control and it is not presented as one: this process exists to
// run whatever the box's owner asks, and a box that would not do that is not a box.
// What the shape does buy is real — no argv length ceiling on a long script, and no
// second layer of quoting between the caller's text and the shell that reads it.
//
// The controls that ARE real are elsewhere and are named where they live: the
// caller held the box's own revocable credential (authed), the process is already
// confined to the box's mount/PID/UTS/IPC namespaces and its chroot (the runtime's
// init), and the box's userland is a tree that is thrown away when the box is
// destroyed. The control NOT present is network isolation, stated at the top of
// this file.
func (b *broker) exec(ctx context.Context, req execRequest) (map[string]any, error) {
	timeout := b.cfg.ExecTimeout
	if req.TimeoutSeconds > 0 {
		if req.TimeoutSeconds > 600 {
			return nil, errors.New("timeout_seconds must be between 1 and 600")
		}
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	workdir := req.WorkingDir
	if workdir == "" {
		workdir = "/srv/app"
	}
	if _, err := os.Stat(workdir); err != nil {
		workdir = "/"
	}

	started := time.Now()
	cmd := exec.CommandContext(ctx, "/bin/sh")
	cmd.Dir = workdir
	cmd.Stdin = strings.NewReader(execPrelude + req.Command + "\n")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	exitCode := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return nil, fmt.Errorf("run: %w", err)
		}
		exitCode = ee.ExitCode()
	}
	// A command killed by the deadline is reported as a timeout rather than as an
	// ordinary non-zero exit: "exit 143" tells the caller nothing about whether
	// their build failed or simply did not fit in the window.
	timedOut := ctx.Err() != nil
	return map[string]any{
		"exit_code":   exitCode,
		"stdout":      stdout.String(),
		"stderr":      stderr.String(),
		"duration_ms": time.Since(started).Milliseconds(),
		"timed_out":   timedOut,
		"box":         b.cfg.BoxName,
		"served_by":   "box-broker (inside the box)",
	}, nil
}

// --- MCP Streamable HTTP -----------------------------------------------------

// The box speaks MCP itself, on its own endpoint, so the customer's agent connects
// to the BOX rather than to us. That is the whole point of D6: the platform's MCP
// surface curates its tools by an allowlist that contains no exec verb, and this is
// where the exec verb legitimately lives instead.
//
// This is a deliberately small implementation of the Streamable HTTP transport: one
// POST endpoint, JSON responses, no SSE and no session resumption. A box's agent
// connection is a short-lived thing over loopback (or, in production, over the box's
// own hostname), so streams that survive reconnects buy nothing here — and a
// half-implemented resumption story is worse than an absent one.

const mcpProtocolVersion = "2025-03-26"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (b *broker) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, rpcError(nil, -32700, "parse error: "+err.Error()))
		return
	}
	// A notification has no id and gets no body: answering one with a result is a
	// protocol error that some clients treat as an unsolicited response.
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "initialize":
		writeJSON(w, http.StatusOK, rpcResult(req.ID, map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    "dada-box",
				"version": "1",
			},
			"instructions": "This is the box's own endpoint. Commands run inside the box; the Dada control plane is not on the path.",
		}))
	case "tools/list":
		writeJSON(w, http.StatusOK, rpcResult(req.ID, map[string]any{"tools": mcpTools()}))
	case "tools/call":
		b.handleToolCall(w, req)
	default:
		writeJSON(w, http.StatusOK, rpcError(req.ID, -32601, "method not found: "+req.Method))
	}
}

func mcpTools() []map[string]any {
	return []map[string]any{
		{
			"name":        "run_command",
			"description": "Run a shell command inside this box and return its exit code, stdout and stderr. The box's env (including any attached database credentials) is sourced first.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command":         map[string]any{"type": "string", "description": "The shell command to run."},
					"working_dir":     map[string]any{"type": "string", "description": "Working directory; defaults to /srv/app."},
					"timeout_seconds": map[string]any{"type": "integer", "description": "1-600; defaults to 60."},
				},
				"required": []string{"command"},
			},
		},
		{
			"name":        "box_info",
			"description": "Report which box this endpoint belongs to and what isolation it actually has.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

func (b *broker) handleToolCall(w http.ResponseWriter, req rpcRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSON(w, http.StatusOK, rpcError(req.ID, -32602, "invalid params: "+err.Error()))
		return
	}
	switch params.Name {
	case "box_info":
		writeJSON(w, http.StatusOK, rpcResult(req.ID, toolText(b.info(), false)))
	case "run_command":
		var args execRequest
		if len(params.Arguments) > 0 {
			if err := json.Unmarshal(params.Arguments, &args); err != nil {
				writeJSON(w, http.StatusOK, rpcError(req.ID, -32602, "invalid arguments: "+err.Error()))
				return
			}
		}
		if strings.TrimSpace(args.Command) == "" {
			writeJSON(w, http.StatusOK, rpcError(req.ID, -32602, "command is required"))
			return
		}
		res, err := b.exec(context.Background(), args)
		if err != nil {
			writeJSON(w, http.StatusOK, rpcError(req.ID, -32603, err.Error()))
			return
		}
		// A non-zero exit is isError:true rather than a protocol error. The call
		// succeeded; the customer's command is what failed, and collapsing the two
		// would make a failing build indistinguishable from a broken box.
		failed := res["exit_code"].(int) != 0
		writeJSON(w, http.StatusOK, rpcResult(req.ID, toolText(res, failed)))
	default:
		writeJSON(w, http.StatusOK, rpcError(req.ID, -32602, "unknown tool: "+params.Name))
	}
}

func toolText(payload map[string]any, isError bool) map[string]any {
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		body = []byte(fmt.Sprintf("%v", payload))
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(body)}},
		"isError": isError,
	}
}

func rpcResult(id json.RawMessage, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func rpcError(id json.RawMessage, code int, message string) map[string]any {
	out := map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": code, "message": message}}
	if len(id) > 0 {
		out["id"] = id
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
