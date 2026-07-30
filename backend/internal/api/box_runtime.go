package api

import (
	"context"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/box"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/metrics"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// Box runtime wiring.
//
// The whole box runtime is behind ONE switch, BOX_LOCAL_ROOT. Unset means the box
// verbs answer 503 with a reason, exactly like every other unconfigured subsystem
// in this handler (Portainer, Kanister, the S3 resolvers). It is a switch and not a
// build tag because the same binary has to run in a cluster where the production
// adapter is the one wired in.
//
// The name in the config is BOX_LOCAL_ROOT, not BOX_ROOT, so a production values
// file cannot enable the local adapter by accident while reading as if it enabled
// "the box runtime".

// boxRuntimeStack is everything the box verbs need. It is either fully wired or
// nil; a half-wired stack would answer some verbs and 500 on others.
type boxRuntimeStack struct {
	runtime  *box.LocalRuntime
	pool     *box.MemoryPool
	attach   *box.LocalAttachProvider
	exposer  *box.LocalExposer
	image    string
	region   string
	sessions string // base URL of the box session surface (the broker's stand-in)
	warmed   bool
}

// initBoxRuntime wires the local box runtime when BOX_LOCAL_ROOT is set.
//
// Warming is SYNCHRONOUS and that is deliberate. The warm pool is the cold path
// made explicit: creating a box's tree and bringing its container daemon up costs
// seconds, and the product's central claim is that a claim does not. Doing it in a
// background goroutine would make the first spawn after start a pool miss, which
// is a latency cliff hidden behind a fast startup — the exact thing
// ErrPoolExhausted exists to keep visible.
func (h *Handler) initBoxRuntime(cfg *config.Config) {
	if cfg.BoxLocalRoot == "" {
		log.Info().Msg("box: runtime disabled (BOX_LOCAL_ROOT unset); box verbs answer 503")
		return
	}
	rt := box.NewLocalRuntime(cfg.BoxLocalRoot, box.SystemClock{})
	// The box's own door (D6). Set before Warm, because the bind that makes the
	// binary visible inside a box happens in that box's init — a broker directory
	// attached after warming would only reach boxes created later, which is the kind
	// of half-configured fleet that produces "it works on the second box".
	rt.BrokerDir = cfg.BoxBrokerDir
	pool := box.NewMemoryPool()
	stack := &boxRuntimeStack{
		runtime: rt,
		pool:    pool,
		attach: &box.LocalAttachProvider{
			Runtime:       rt,
			AdminDSN:      cfg.BoxManagedPGURL,
			ReachableHost: cfg.BoxManagedPGHost,
			ReachablePort: cfg.BoxManagedPGPort,
		},
		exposer:  box.NewLocalExposer(cfg.BoxHostnameBase),
		image:    cfg.BoxWarmImage,
		region:   cfg.BoxRegion,
		sessions: boxSessionBaseURL(cfg),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	started := time.Now()
	if err := rt.Warm(ctx, pool, stack.image, stack.region, cfg.BoxWarmPoolSize); err != nil {
		// A pool that could not be warmed is reported and left empty rather than
		// fatal: the API still answers, and a spawn returns pool_exhausted, which is
		// the honest classification.
		log.Error().Err(err).Msg("box: warming the pool failed; spawns will report pool_exhausted")
	} else {
		log.Info().
			Str("root", cfg.BoxLocalRoot).
			Str("image", stack.image).
			Int("warm", cfg.BoxWarmPoolSize).
			Bool("broker", rt.BrokerConfigured()).
			Dur("took", time.Since(started)).
			Msg("box: LocalRuntime warm pool ready")
		stack.warmed = true
	}
	if !rt.BrokerConfigured() {
		// Said out loud at start rather than discovered per box. Without a broker a
		// box has no endpoint of its own, so every agent connection goes through the
		// control plane — which is the fallback, not the product (D6).
		log.Warn().Str("dir", cfg.BoxBrokerDir).
			Msg("box: no broker binary (BOX_BROKER_DIR); boxes will have no endpoint of their own and `box up` will say so")
	}
	metrics.SetBoxPoolGauges(stack.image, stack.region,
		pool.Available(stack.image, stack.region), pool.Target(stack.image, stack.region))
	h.boxStack = stack
}

// boxSessionBaseURL is where the box's own session surface lives. Derived from the
// same loopback URL the embedded MCP proxy uses so there is one answer to "where
// does this process answer requests".
func boxSessionBaseURL(cfg *config.Config) string {
	if cfg.BoxSessionBaseURL != "" {
		return cfg.BoxSessionBaseURL
	}
	if cfg.MCPSelfURL != "" {
		return cfg.MCPSelfURL
	}
	return "http://127.0.0.1:" + cfg.Port
}

// requireBoxRuntime answers 503 when the box runtime is not configured.
func (h *Handler) requireBoxRuntime(c *gin.Context) (*boxRuntimeStack, bool) {
	if h.boxStack == nil {
		respondError(c, http.StatusServiceUnavailable,
			"box runtime not configured: set BOX_LOCAL_ROOT to run boxes on this host, or deploy the cluster adapter (ADR-019)")
		return nil, false
	}
	return h.boxStack, true
}

// publishBoxPoolGauges refreshes the warm-pool gauges after a claim.
//
// They are written HERE and nowhere else, because only the pool knows both numbers.
// The boxes table cannot answer them: its rows are claimed boxes, and a warm slot
// is by definition not claimed, so counting live boxes under the name "available"
// would publish nearly the inverse of the truth.
func (s *boxRuntimeStack) publishBoxPoolGauges() {
	metrics.SetBoxPoolGauges(s.image, s.region,
		s.pool.Available(s.image, s.region), s.pool.Target(s.image, s.region))
}

// instanceFor rebuilds the runtime handle for a stored box.
//
// The refs are opaque handles owned by the runtime; the control plane relays them
// and never interprets them, which is why this is a plain projection of the row
// rather than anything that parses instance_ref.
func instanceFor(boxID, instanceRef, nodeRef, image, region, sshHost string, sshPort *int, mcpURL string) *box.Instance {
	inst := &box.Instance{
		ID:          boxID,
		InstanceRef: instanceRef,
		NodeRef:     nodeRef,
		Image:       image,
		Region:      region,
		SSHHost:     sshHost,
		MCPURL:      mcpURL,
	}
	if sshPort != nil {
		inst.SSHPort = *sshPort
	}
	return inst
}
