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
//
// The fields are INTERFACES, not the local adapter's types, and that is the point
// of this struct: the handlers are the control plane, and a control plane that
// names a concrete runtime cannot be pointed at a second one. The cluster adapter
// (ADR-019) is a different implementation of the same five seams, so wiring it is
// a change to initBoxRuntime and to nothing else.
//
// `local` is the exception, and it is deliberately the ugly one. Two paths still
// need more than the seams offer — the control-plane session surface (the box's
// door when no broker started) and the crystallizer's stand-in — and both are
// LocalRuntime-only by construction. Naming them in one field, set only when the
// wired runtime IS the local adapter, keeps that dependency countable: when the
// cluster adapter arrives, `local` is nil, those two paths answer 503 with a
// reason, and nothing else in the package notices. A stack that hid this behind an
// interface it cannot satisfy would fail at run time instead of at wiring time.
type boxRuntimeStack struct {
	runtime  box.BoxRuntime
	pool     box.WarmPool
	attach   box.AttachProvider
	exposer  box.Exposer
	door     box.Door
	local    *box.LocalRuntime
	cryst    reportingCrystallizer
	image    string
	region   string
	sessions string // base URL of the box session surface (the broker's stand-in)
	warmed   bool
}

// reportingCrystallizer is the crystallizer the control plane actually needs.
//
// box.Crystallizer returns the carry manifest alone, and the carry manifest is
// the smaller half of the answer: ADR-019 §7 makes the verification report the
// deliverable. Both adapters implement this, so the handler holds one interface
// and neither adapter's type is named outside its wiring.
type reportingCrystallizer interface {
	CrystallizeWithReport(ctx context.Context, inst *box.Instance, opts box.CrystallizeOptions) (*box.CrystallizationReport, error)
}

// requireCrystallizer answers 503 when no adapter can promote a box, rather than
// letting the handler dereference nil.
func (s *boxRuntimeStack) requireCrystallizer(c *gin.Context) (reportingCrystallizer, bool) {
	if s.cryst == nil {
		respondError(c, http.StatusServiceUnavailable,
			"the wired box runtime cannot crystallize: no crystallizer is configured (ADR-019)")
		return nil, false
	}
	return s.cryst, true
}

// requireLocalRuntime answers 503 for the two paths that only the local adapter
// can serve, naming which one the caller asked for.
func (s *boxRuntimeStack) requireLocalRuntime(c *gin.Context, what string) (*box.LocalRuntime, bool) {
	if s.local == nil {
		respondError(c, http.StatusServiceUnavailable,
			"the wired box runtime does not serve "+what+": it is available on the local adapter only (ADR-019)")
		return nil, false
	}
	return s.local, true
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
		if cfg.BoxClusterNamespace != "" {
			h.initClusterBoxRuntime(cfg)
			return
		}
		log.Info().Msg("box: runtime disabled (BOX_LOCAL_ROOT and BOX_CLUSTER_NAMESPACE unset); box verbs answer 503")
		return
	}
	if cfg.BoxClusterNamespace != "" {
		log.Warn().Str("namespace", cfg.BoxClusterNamespace).
			Msg("box: BOX_LOCAL_ROOT and BOX_CLUSTER_NAMESPACE are both set; using the local adapter and ignoring the cluster one")
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
		door:    rt,
		local:   rt,
		cryst:   &box.LocalCrystallizer{Runtime: rt, Clock: box.SystemClock{}},
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

// initClusterBoxRuntime wires the production adapter: a box is a Pod in the
// existing cluster (ADR-019).
//
// Two seams stay unwired here and that is deliberate rather than unfinished.
// `local` is nil, so the control-plane session fallback and the crystallizer
// answer 503 naming themselves — the boxRuntimeStack doc explains why that is
// countable instead of hidden. `attach` is nil for the same reason: managed
// Postgres for a cluster box means a real ServiceDatabaseV2, not a database
// created inside a disposable body, and shipping the local provider here would
// hand the tenant a DSN that resolves to nothing.
//
// Warming is BACKGROUND here, and that is the one place this path departs from
// the local adapter on purpose. The local warm costs seconds, so paying it before
// the server answers is honest. A cluster warm costs minutes — the first pod on a
// node pulls a multi-GB image — and the console backend runs behind a startup
// probe that kills a process which has not answered by then. A synchronous warm
// would therefore not produce a slow start, it would produce a crashloop, and a
// crashlooping console is a worse answer to "the pool is cold" than
// pool_exhausted. The cost is not hidden: a spawn before the pool fills still
// reports pool_exhausted, and the gauges start at zero and are refreshed on every
// fill. The fill also does not stop after boot — see boxPoolFillLoop.
func (h *Handler) initClusterBoxRuntime(cfg *config.Config) {
	rt, err := box.NewClusterRuntime(cfg.BoxClusterNamespace, box.SystemClock{})
	if err != nil {
		log.Error().Err(err).Str("namespace", cfg.BoxClusterNamespace).
			Msg("box: cluster adapter requested but no in-cluster client could be built; box verbs answer 503")
		return
	}
	rt.PullSecret = cfg.BoxClusterPullSecret
	if cfg.BoxClusterStorageClass != "" {
		rt.StorageClass = cfg.BoxClusterStorageClass
	}

	exposer := box.NewClusterExposer(rt, cfg.BoxHostnameBase)
	if cfg.BoxClusterTLSSecret != "" {
		exposer.TLSSecret = cfg.BoxClusterTLSSecret
	}

	pool := box.NewClusterPool(rt)
	cryst := box.NewClusterCrystallizer(rt, cfg.BoxHostnameBase)
	if cfg.BoxClusterTLSSecret != "" {
		cryst.TLSSecret = cfg.BoxClusterTLSSecret
	}
	stack := &boxRuntimeStack{
		runtime:  rt,
		door:     rt,
		pool:     pool,
		cryst:    cryst,
		exposer:  exposer,
		image:    cfg.BoxWarmImage,
		region:   cfg.BoxRegion,
		sessions: boxSessionBaseURL(cfg),
	}

	metrics.SetBoxPoolGauges(stack.image, stack.region,
		pool.Available(stack.image, stack.region), pool.Target(stack.image, stack.region))
	h.boxStack = stack

	if reaped, err := rt.ReapOrphans(context.Background()); err != nil {
		log.Warn().Err(err).Msg("box: startup orphan sweep failed")
	} else if reaped > 0 {
		log.Warn().Int("reaped", reaped).
			Msg("box: startup orphan sweep removed pods/PVCs a previous run abandoned before they became ready")
	}
	go h.boxReapLoop(rt)

	go boxPoolFillLoop(rt, pool, stack.image, stack.region, cfg.BoxWarmPoolSize)
}

// clusterPoolFillInterval is how often the running process reconciles the warm
// pool back up to its target.
//
// A minute is chosen against the two failures it has to cover, not against a
// guess. The first is a claim: the pool drops by one the moment a box is handed
// over, and the next customer should not inherit that hole. The second is a
// transient create failure — an image tag that does not exist yet, a storage
// quota momentarily full, a node with no room — which at boot used to be
// permanent, because warming ran exactly once and a process that failed it
// served pool_exhausted until someone restarted it. A minute is far shorter than
// a customer's patience and far longer than a pod create, and refills cost
// nothing while the pool is full: Warm reconciles to the target and returns
// immediately when there is no deficit.
const clusterPoolFillInterval = time.Minute

// boxPoolFillLoop keeps the warm pool at its target for the life of the process.
// It fills once immediately — a cold pool at boot is the common case and waiting
// out the first tick would add a minute to it for no reason — and then reconciles
// on the ticker.
//
// Like the orphan sweep it has no cancellation: the process exiting is what stops
// it. Failures are logged and retried on the next tick rather than ending the
// loop, because the conditions that make a create fail are exactly the ones that
// pass on their own.
func boxPoolFillLoop(rt *box.ClusterRuntime, pool box.ParkingPool, image, region string, target int) {
	fill := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		started := time.Now()
		before := pool.Available(image, region)
		if err := rt.Warm(ctx, pool, image, region, target); err != nil {
			log.Error().Err(err).Int("available", pool.Available(image, region)).Int("target", target).
				Msg("box: filling the cluster pool failed; spawns report pool_exhausted until the next attempt")
		} else if changed := pool.Available(image, region) - before; changed != 0 {
			msg := "box: cluster warm pool filled"
			if changed < 0 {
				msg = "box: cluster warm pool trimmed back to target; surplus warm boxes were holding fleet quota"
			}
			log.Info().
				Str("namespace", rt.Namespace).
				Str("image", image).
				Int("changed", changed).
				Int("available", pool.Available(image, region)).
				Int("target", target).
				Dur("took", time.Since(started)).
				Msg(msg)
		}
		metrics.SetBoxPoolGauges(image, region, pool.Available(image, region), pool.Target(image, region))
	}

	fill()
	ticker := time.NewTicker(clusterPoolFillInterval)
	defer ticker.Stop()
	for range ticker.C {
		fill()
	}
}

// clusterOrphanReapInterval is how often the running process re-sweeps the box
// namespace for pods that never went Ready. It runs independently of the fill
// loop: a pod can be orphaned any time a process is restarted mid-create, and the
// process that would have cleaned it up is the one that died, so the sweep has to
// be somebody else's job and has to keep going for the whole process life.
const clusterOrphanReapInterval = 2 * time.Minute

// boxReapLoop runs both namespace sweeps on a ticker for as long as the process
// lives. It has no cancellation because it has nothing to hand back and nothing
// to wait on: the process exiting is what stops it, the same way the warm
// goroutine above stops.
//
// Two sweeps rather than one because they answer different questions and neither
// can answer the other's. ReapOrphans asks whether a pod ever came up, which needs
// no database and catches a create that died mid-flight within minutes.
// ReapUnclaimed asks whether the control plane still owns an object at all, which
// needs the database and catches everything the first sweep structurally cannot
// see — crystallization Deployments, published Services and Ingresses, and the
// workspace volumes of boxes that are already tombstones.
//
// NEITHER TAKES THE BOX-REAPER ADVISORY LOCK, unlike the lifecycle sweep in
// box_reaper.go, and the difference is what each one does rather than how often it
// runs. The lifecycle sweep sends customer mail and enqueues operations, so three
// replicas would mean three emails; these two only delete Kubernetes objects, and
// a delete that loses the race gets NotFound and is tolerated. Running them
// unguarded on every replica is therefore free, and it means a leak keeps being
// collected while whichever replica holds the lock is wedged.
func (h *Handler) boxReapLoop(rt *box.ClusterRuntime) {
	ticker := time.NewTicker(clusterOrphanReapInterval)
	defer ticker.Stop()
	for range ticker.C {
		reaped, err := rt.ReapOrphans(context.Background())
		if err != nil {
			log.Warn().Err(err).Msg("box: orphan sweep failed")
		} else if reaped > 0 {
			log.Warn().Int("reaped", reaped).Msg("box: orphan sweep removed pods/PVCs that never went ready")
		}
		h.sweepUnclaimedBoxObjects(rt)
	}
}

// sweepUnclaimedBoxObjects runs one ownership sweep of the box namespace.
//
// The database read is what makes this pass possible and also what can make it
// dangerous, so a failed read ends the pass rather than degrading it: without a
// complete live set there is no difference between "nothing is claimed" and "the
// database did not answer", and box.ReapUnclaimed refuses the second reading on
// its own as well. A skipped sweep costs one tick of leak; a sweep on a bad
// reading costs the fleet.
func (h *Handler) sweepUnclaimedBoxObjects(rt *box.ClusterRuntime) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	live, err := boxLiveObjects(ctx, h.pool)
	if err != nil {
		log.Warn().Err(err).Msg("box: cannot read what the control plane still owns; skipping the ownership sweep")
		return
	}
	report, err := rt.ReapUnclaimed(ctx, live)
	if err != nil {
		log.Warn().Err(err).Str("removed", report.String()).Msg("box: ownership sweep hit an error")
	}
	if report.Total() > 0 {
		log.Warn().Str("removed", report.String()).Int("total", report.Total()).
			Int("live_boxes", len(live.InstanceRefs)).Int("live_crystals", len(live.Crystals)).
			Msg("box: ownership sweep removed objects no database row accounts for")
	}
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

// requireAttachProvider answers 503 when the wired adapter has no attach path.
//
// The cluster adapter deliberately has none yet: managed Postgres for a cluster
// box means a real ServiceDatabaseV2 outside the box, and the local provider
// would hand the tenant a DSN pointing at a database inside a body that is about
// to be destroyed. An unconfigured subsystem is not a failed request, which is
// why this is 503 with a reason and not a 500.
//
// The message deliberately does not say "yet" or "not ready": on the cluster
// adapter this is not a warm-up state a retry will clear, it is the shape of
// this installation today (see boxRuntimeStack doc). A caller — human or agent —
// that reads "yet" retries forever. The message instead names the working path:
// createDatabase against the project's environment provisions the same managed
// Postgres outside the box; the caller injects its credential into the box itself
// (getBoxConnection plus its own env-setting step).
func (s *boxRuntimeStack) requireAttachProvider(c *gin.Context) (box.AttachProvider, bool) {
	if s.attach == nil {
		respondError(c, http.StatusServiceUnavailable,
			"attaching a managed database directly to a box is not available in this installation: the wired box runtime has no attach path (ADR-019). This is not temporary and will not succeed on retry. Use a project database instead: POST /projects/{projectId}/environments/{envId}/databases (MCP tool createDatabase), then set its connection details as env inside the box yourself")
		return nil, false
	}
	return s.attach, true
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
