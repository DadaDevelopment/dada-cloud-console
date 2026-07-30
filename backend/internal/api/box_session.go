package api

import (
	"context"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/box"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
)

// The box's own door.
//
// DELIBERATELY UN-ANNOTATED, and registered only inside the `if the box runtime is
// configured` guard in router.go — the same construction as webhooks_boxagent.go,
// for the same two reasons, and both are load-bearing:
//
//   - openapi_coverage_test.go enumerates the routes SetupRouter actually
//     registers under the test config. That config sets no BOX_LOCAL_ROOT, so these
//     routes are not registered there and the coverage gate has nothing to demand a
//     spec entry for.
//   - the MCP tool surface is REFLECTED from swagger.json, and the standalone
//     mcp-server curates by DENYLIST, so anything reaching the spec becomes a tool
//     there by default. An annotated exec endpoint would therefore publish
//     "run a command in a box" as an agent-callable tool on OUR control plane.
//
// That last one is a product decision, not a security nicety (D6). The customer's
// agent keeps its brain local and talks to the box's own endpoint, so their source
// and their model credentials never traverse our API. A boxExec tool here would
// route every keystroke of their work through us, which is the opposite of what the
// product promises. There is no such tool, there is no keep-list entry that could
// create one, and this endpoint cannot become one.
//
// WHAT THIS IS NOW: the FALLBACK. cmd/box-broker exists and runs inside the box, and
// `box up` publishes the box's own endpoint whenever it came up — so on a configured
// host the customer's agent never reaches this handler at all. It is kept, and kept
// working, for the box whose broker did not start or whose host has no BOX_BROKER_DIR:
// deleting the only working door in the change that adds a new one is how a walkable
// path stops being walkable.
//
// Which of the two a given box got is not left to be discovered. The connect block
// computes `mcp.available` from the published URL, and when it is false it says
// plainly that commands DO pass through us and that this is a degraded box rather
// than the product.
//
// AUTHENTICATION IS THE BOX'S OWN CREDENTIAL, never a console session: the
// "dadabox_" token from `box up`, resolved by sha256 against box_sessions. A user
// JWT is deliberately not accepted — the door belongs to the box.

type boxSessionExecRequest struct {
	// Command is a shell command run inside the box, with the box's env sourced
	// and cwd inside its tree.
	Command string `json:"command"`
	// Background turns the command into a supervised service. ServiceName and Ports
	// are then required, because a long-running process with no declared ports and
	// no name is a process crystallization cannot render into a unit.
	Background  bool   `json:"background"`
	ServiceName string `json:"service_name"`
	Ports       []int  `json:"ports"`
	WorkingDir  string `json:"working_dir"`
	// TimeoutSeconds bounds one command, in [1,600].
	TimeoutSeconds int `json:"timeout_seconds"`
}

// BoxSessionExec runs one command inside the box the session token belongs to.
// Intentionally carries no swaggo annotation — see the file comment.
func (h *Handler) BoxSessionExec(c *gin.Context) {
	stack, ok := h.requireBoxRuntime(c)
	if !ok {
		return
	}
	s, ok := h.boxSessionFromToken(c)
	if !ok {
		return
	}
	var req boxSessionExecRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.Command == "" {
		respondError(c, http.StatusBadRequest, "command is required")
		return
	}
	if req.Background && req.ServiceName == "" {
		respondError(c, http.StatusBadRequest,
			"service_name is required for a background command: crystallization renders one systemd unit per named service, and an unnamed process cannot be carried")
		return
	}
	if req.Background && len(req.Ports) == 0 {
		respondError(c, http.StatusBadRequest,
			"ports is required for a background command: the listening-socket set is what crystallization verifies against, and an undeclared port cannot be checked")
		return
	}
	if req.ServiceName != "" {
		if err := validateKubeName(req.ServiceName); err != nil {
			respondError(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}
	if timeout > 600 {
		respondError(c, http.StatusBadRequest, "timeout_seconds must be between 1 and 600")
		return
	}
	// A frozen or torn-down box must not execute anything, and the check is on the
	// box's phase rather than on the token: a token stays valid for its own lifetime,
	// while the body it opens can go away underneath it.
	if models.BoxStatus(s.Status) != models.BoxStatusReady && models.BoxStatus(s.Status) != models.BoxStatusIdle {
		respondError(c, http.StatusConflict, "the box is in phase "+s.Status+" and cannot run commands")
		return
	}
	if s.InstanceRef == "" {
		respondError(c, http.StatusConflict, "the box has no runtime instance")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(timeout)*time.Second)
	defer cancel()

	inst := &box.Instance{ID: s.BoxID.String(), InstanceRef: s.InstanceRef}
	started := time.Now()

	if req.Background {
		if err := stack.runtime.StartService(ctx, inst, req.ServiceName, req.Command, req.WorkingDir, req.Ports); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to start the service: "+err.Error())
			return
		}
		// Report which declared ports actually came up, by connecting to them FROM
		// INSIDE the box. A response that said "started" without that would be the
		// same lie as a readiness check that stops at "the process was spawned".
		listening := waitForBoxPorts(ctx, stack.runtime, inst, req.Ports, 10*time.Second)
		c.JSON(http.StatusOK, gin.H{
			"service":     req.ServiceName,
			"ports":       req.Ports,
			"listening":   listening,
			"duration_ms": time.Since(started).Milliseconds(),
			"descriptor":  "/etc/dada/services/" + req.ServiceName + ".json (inside the box; crystallization reads it)",
		})
		return
	}

	res, err := stack.runtime.Run(ctx, inst, req.Command)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to run the command: "+err.Error())
		return
	}
	// Activity: the box was used, so the idle reaper's clock moves. Best-effort —
	// a failed touch must not fail the caller's command.
	_, _ = h.pool.Exec(c.Request.Context(),
		`UPDATE boxes SET last_active_at = now(), updated_at = now() WHERE id = $1`, s.BoxID)

	c.JSON(http.StatusOK, gin.H{
		"exit_code":   res.ExitCode,
		"stdout":      res.Stdout,
		"stderr":      res.Stderr,
		"duration_ms": time.Since(started).Milliseconds(),
		"box":         s.BoxName,
	})
}

// waitForBoxPorts polls the declared ports from inside the box until they all
// answer or the window closes, and returns the ones that did.
func waitForBoxPorts(ctx context.Context, rt *box.LocalRuntime, inst *box.Instance, ports []int, within time.Duration) []int {
	deadline := time.Now().Add(within)
	var last []int
	for {
		got, err := rt.ListeningPorts(ctx, inst, ports)
		if err == nil {
			last = got
			if len(got) == len(ports) {
				return got
			}
		}
		if time.Now().After(deadline) {
			return last
		}
		select {
		case <-ctx.Done():
			return last
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// BoxSessionInfo reports what the session's box is, for a client that has a token
// and wants to confirm what it opens. Intentionally un-annotated — see the file
// comment.
func (h *Handler) BoxSessionInfo(c *gin.Context) {
	if _, ok := h.requireBoxRuntime(c); !ok {
		return
	}
	s, ok := h.boxSessionFromToken(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"box":          s.BoxName,
		"status":       s.Status,
		"instance_ref": s.InstanceRef,
		"exec":         "POST /api/v1/box/session/exec",
		"note": "this is the box's own door, served here because LocalRuntime has no broker. " +
			"In production it is cmd/box-broker at the box's own hostname (backlog phase 4).",
	})
}
