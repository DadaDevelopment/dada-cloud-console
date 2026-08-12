package api

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// appHealthWatchInterval is the poll period for the silent-crash watcher: a
// user's app crashlooping/OOMKilled/ImagePullBackOff otherwise goes unnoticed
// until they happen to open the console (P1-2b). Not a new deploy/cronjob —
// just a ticker goroutine inside the existing backend pod.
const appHealthWatchInterval = 3 * time.Minute

// appHealthAlertCooldown caps one alert email per app per window, so a stuck
// crashloop does not spam the owner's inbox every tick.
const appHealthAlertCooldown = 24 * time.Hour

// appHealthAlertRetryBackoff is how much of the 24h cooldown a failed send
// gives back. The claim happens before the send (see claimAppHealthAlertSlot
// and maybeNotify), so a send that errors out must not simply re-arm the
// full cooldown for "now" — that would fire the very next tick and turn a
// flaky SMTP relay into a retry storm, which is exactly what claiming before
// sending was built to prevent. But leaving the full 24h burned on a send
// that never reached the owner is its own bug (the one this migration
// fixes): an app that stays broken would then go silent for a full day on
// nothing but a transient mail-relay hiccup. Rolling last_sent_at back by
// this fixed amount splits the difference: the next attempt is a bounded 15
// minutes out, not immediate and not a day away.
const appHealthAlertRetryBackoff = 15 * time.Minute

// appHealthAlertFreshWindow is how recently last_seen_at must have been
// touched for the console to still show a crash alert as current
// (P1-ALERTS-IN-UI-FRESHNESS). Tied to appHealthWatchInterval (3m) with a 5x
// margin: the watcher touches last_seen_at on every tick it still detects
// the bad state, so a genuinely ongoing crash is re-touched every 3m and
// never falls out of a 15m window, while an app fixed 10 minutes ago clears
// the banner well before the old 24h cooldown row would have. If
// appHealthWatchInterval ever changes, this margin must move with it — a
// window narrower than a few tick periods risks a fresh crash briefly
// reading as resolved between ticks.
const appHealthAlertFreshWindow = 5 * appHealthWatchInterval

// appHealthRecoveryWindow is how long a container must have been running and
// ready before the crash it recovered from stops counting as a live incident
// (see containerRecovered). Same 5x margin on the tick as the freshness
// window, for the same reason — a container that flaps within a few tick
// periods must never slip through as recovered between two crashes — but a
// separate constant on purpose: this one answers "has the container healed",
// not "is the stored alert row still current", and the two are free to move
// apart.
const appHealthRecoveryWindow = 5 * appHealthWatchInterval

// appHealthLogTailLines bounds the best-effort log excerpt attached to the
// alert email.
const appHealthLogTailLines = 20

// appHealthBadReasons are the container states worth alerting an owner about.
// Anything else (Pending on a slow pull, normal Running) is left alone.
const (
	reasonOOMKilled        = "OOMKilled"
	reasonCrashLoopBackOff = "CrashLoopBackOff"
	reasonImagePullBackOff = "ImagePullBackOff"
	reasonErrImagePull     = "ErrImagePull"
	reasonError            = "Error"
)

// emailableReason gates which detected reasons still send an owner email.
// reasonError (a plain non-zero exit, no named waiting reason involved) is
// deliberately excluded: the owner asked to stop being emailed on every
// crash, so this class is recorded in app_health_alerts and shown in the
// console UI, same as the other four, but never mailed. The other four keep
// their existing email behaviour unchanged.
func emailableReason(reason string) bool {
	switch reason {
	case reasonOOMKilled, reasonCrashLoopBackOff, reasonImagePullBackOff, reasonErrImagePull:
		return true
	default:
		return false
	}
}

// appHealthAlert is one detected bad-state container, ready to notify on.
// ExitCode is only meaningful when Reason is reasonError.
type appHealthAlert struct {
	Namespace string
	AppName   string
	PodName   string
	Container string
	Reason    string
	ExitCode  int32
}

// appHealthWatcher polls user-project namespaces for pods stuck in a bad
// state and emails the project owner once per app per cooldown window. The
// cooldown lives in the app_health_alerts table (not in memory) so it holds
// across pod restarts and across replicas.
type appHealthWatcher struct {
	clientset kubernetes.Interface
	h         *Handler
}

// newAppHealthClientset mirrors the rest.InClusterConfig() +
// kubernetes.NewForConfig pattern used elsewhere (cloudtask/s3creds.go).
// Off-cluster (local dev, no service-account mount) it returns nil and the
// watcher is disabled.
func newAppHealthClientset() kubernetes.Interface {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil
	}
	return cs
}

// StartAppHealthWatcher launches the silent-crash watcher goroutine. No-op
// when mail is unconfigured (nothing to send) or off-cluster (nothing to
// watch), so local dev and tests never spawn it. The first scan runs
// immediately, not after the first interval, matching StartCostCacheWarmer.
func (h *Handler) StartAppHealthWatcher(ctx context.Context) {
	if h.auditNotifier == nil {
		return
	}
	clientset := newAppHealthClientset()
	if clientset == nil {
		log.Printf("app-health: no in-cluster client, watcher disabled")
		return
	}
	w := &appHealthWatcher{clientset: clientset, h: h}
	go func() {
		runWithAdvisoryLock(ctx, h.pool, lockKeyAppHealthWatch, "app-health", w.tick)
		t := time.NewTicker(appHealthWatchInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runWithAdvisoryLock(ctx, h.pool, lockKeyAppHealthWatch, "app-health", w.tick)
			}
		}
	}()
}

// namespaceEnv is one k8s environment row scoped to a project, enough to
// build a console deep link and resolve the owner.
type namespaceEnv struct {
	ProjectID uuid.UUID
}

// namespaceProjects returns the set of live k8s project namespaces this
// watcher is allowed to scan, keyed by namespace. It reuses the exact
// user-vs-infra boundary already established by billing_fullcost.go's
// userNamespaces: only namespaces recorded against a project's k8s
// environment are user workloads, everything else (argocd, longhorn,
// monitoring, ...) is shared platform infra and is never scanned.
func (h *Handler) namespaceProjects(ctx context.Context) (map[string]namespaceEnv, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT namespace, project_id FROM environments
		 WHERE runtime = 'k8s' AND namespace <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]namespaceEnv{}
	for rows.Next() {
		var ns string
		var projectID uuid.UUID
		if err := rows.Scan(&ns, &projectID); err != nil {
			return nil, err
		}
		out[ns] = namespaceEnv{ProjectID: projectID}
	}
	return out, rows.Err()
}

// Alert recipient sources, logged at send time (P1-ALERT-OWNERLESS-DROP) so
// every alert email is traceable to exactly which step of the resolver chain
// picked the address, and the operator-fallback case (no reachable owner) is
// distinguishable from the normal case in logs alone.
const (
	alertSourceOwner       = "owner"
	alertSourceMember      = "member"
	alertSourcePersonalOrg = "personal-org"
	alertSourceOperator    = "operator-fallback"
)

// isKeycloakLocalEmail reports whether email is one of the synthetic
// placeholder addresses internal/auth/provision.go stamps on a Keycloak
// identity with no email claim (<sub>@keycloak.local). Such an address
// resolves non-empty but is not a real mailbox: every step of
// resolveAlertRecipient must reject it and fall through to the next
// candidate instead of "sending" an alert into the void.
func isKeycloakLocalEmail(email string) bool {
	return strings.HasSuffix(strings.ToLower(email), "@keycloak.local")
}

// ownerEmailByOwnerID is resolveAlertRecipient step (a): projects.owner_id ->
// users.email, the same LEFT JOIN users ON owner_id pattern used across
// projects.go/admin_overview.go. This is the normal case for every project
// with a single recorded owner.
func (h *Handler) ownerEmailByOwnerID(ctx context.Context, projectID uuid.UUID) string {
	return ownerEmailByOwnerID(ctx, h.pool, projectID)
}

func ownerEmailByOwnerID(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) string {
	var email string
	err := pool.QueryRow(ctx,
		`SELECT u.email FROM projects p
		 JOIN users u ON u.id = p.owner_id
		 WHERE p.id = $1`, projectID).Scan(&email)
	if err != nil || isKeycloakLocalEmail(email) {
		return ""
	}
	return email
}

// ownerEmailByMembers is resolveAlertRecipient step (b): a project_members
// row with role Owner or Admin, picked deterministically (highest role rank
// first, then oldest membership) so repeated ticks always land on the same
// address. A row with a synthetic @keycloak.local email is skipped in favor
// of the next candidate row rather than aborting the whole step.
func (h *Handler) ownerEmailByMembers(ctx context.Context, projectID uuid.UUID) string {
	return ownerEmailByMembers(ctx, h.pool, projectID)
}

func ownerEmailByMembers(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) string {
	rows, err := pool.Query(ctx,
		`SELECT u.email FROM project_members pm
		 JOIN users u ON u.id = pm.user_id
		 WHERE pm.project_id = $1 AND pm.role IN ('Owner', 'Admin')
		 ORDER BY CASE pm.role WHEN 'Owner' THEN 0 WHEN 'Admin' THEN 1 ELSE 2 END,
		          pm.created_at ASC`, projectID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			continue
		}
		if email != "" && !isKeycloakLocalEmail(email) {
			return email
		}
	}
	return ""
}

// ownerEmailByOrgUsername is resolveAlertRecipient step (c): the personal-org
// convention (internal/auth/jwt.go:113-122, every user is implicitly Owner of
// an org whose id equals their username) applied in reverse — a project whose
// org_id matches a user's username is that user's personal project even with
// no owner_id and no project_members row.
func (h *Handler) ownerEmailByOrgUsername(ctx context.Context, projectID uuid.UUID) string {
	return ownerEmailByOrgUsername(ctx, h.pool, projectID)
}

func ownerEmailByOrgUsername(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) string {
	var email string
	err := pool.QueryRow(ctx,
		`SELECT u.email FROM projects p
		 JOIN users u ON u.username = p.org_id
		 WHERE p.id = $1`, projectID).Scan(&email)
	if err != nil || isKeycloakLocalEmail(email) {
		return ""
	}
	return email
}

// resolveAlertRecipient is the single choke point every watcher alert routes
// through to pick a send address: owner_id, then project_members Owner/Admin,
// then the org_id/username personal-org match. Returns ("", "") when none
// resolve to a real mailbox so the caller can fall back to the operator
// address instead of silently dropping the alert — the P1-ALERT-OWNERLESS-DROP
// bug this replaces: 5 live projects have owner_id NULL, one of them
// (client-a-prod) with real crashlooping pods, a real Keycloak user working
// in it, and zero joinable project_members rows, so every alert for it was
// being dropped with no operator visibility at all.
func (h *Handler) resolveAlertRecipient(ctx context.Context, projectID uuid.UUID) (email, source string) {
	return alertRecipientForProject(ctx, h.pool, projectID)
}

// alertRecipientForProject is resolveAlertRecipient with the pool passed in rather
// than reached through a Handler.
//
// The split exists because the Dada Box background loops (box_meter.go,
// box_reaper.go) are package functions with no request, no claims and no Handler —
// and they still have to email a customer when a spend cap suspends their box or a
// reaper is about to destroy it. Giving them their own recipient lookup would have
// been a second implementation of the anti-drop ladder this function IS, and the
// two would have drifted; the P1-ALERT-OWNERLESS-DROP bug in the comment above is
// what that costs. So there is one ladder, and the Handler method is a wrapper.
func alertRecipientForProject(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) (email, source string) {
	if e := ownerEmailByOwnerID(ctx, pool, projectID); e != "" {
		return e, alertSourceOwner
	}
	if e := ownerEmailByMembers(ctx, pool, projectID); e != "" {
		return e, alertSourceMember
	}
	if e := ownerEmailByOrgUsername(ctx, pool, projectID); e != "" {
		return e, alertSourcePersonalOrg
	}
	return "", ""
}

// projectDisplayName best-effort reads a project's name for the operator
// fallback email body (project id + name, so the operator can act on it
// without a console lookup), falling back to the raw id string on any
// failure so a name-lookup problem never blocks the alert itself.
func (h *Handler) projectDisplayName(ctx context.Context, projectID uuid.UUID) string {
	var name string
	if err := h.pool.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, projectID).Scan(&name); err != nil || name == "" {
		return projectID.String()
	}
	return name
}

// tick scans every user namespace once, detects bad-state pods, and fires at
// most one alert per app per cooldown window. Every failure (namespace list
// query, pod list per namespace) is logged and swallowed: one bad namespace
// must never block the scan of the rest, and the watcher must never crash
// the backend pod it runs inside.
func (w *appHealthWatcher) tick(ctx context.Context) {
	nsProjects, err := w.h.namespaceProjects(ctx)
	if err != nil {
		log.Printf("app-health: load namespaces failed: %v", err)
		return
	}
	for ns, env := range nsProjects {
		listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		pods, err := w.clientset.CoreV1().Pods(ns).List(listCtx, metav1.ListOptions{})
		cancel()
		if err != nil {
			log.Printf("app-health: list pods in %s failed: %v", ns, err)
			continue
		}
		for i := range pods.Items {
			alert, bad := detectPodAlert(&pods.Items[i])
			if !bad {
				continue
			}
			w.maybeNotify(ctx, env.ProjectID, alert)
		}
	}
}

// containerRecovered reports whether a container's recorded termination is
// old news rather than a live incident: the container is running right now,
// its readiness probe passes, and it has stayed up for at least window.
//
// This gate exists because LastTerminationState is a permanent record on the
// pod object — it is never cleared while the pod lives. Without the gate, a
// container that crashed once and has been serving happily ever since keeps
// matching the OOMKilled and reasonError branches on every single tick, so
// last_seen_at is refreshed forever and the console shows an owner a red
// "your app crashed" banner on a working app for the pod's entire lifetime
// (observed live 2026-08-11 on artemmendeleev's fonbet-value: pod 1/1 Ready,
// one restart, alert row re-touched every 3 minutes). For OOMKilled — which
// is emailable — it also means one historical OOM mails the owner again every
// time the 24h cooldown lapses.
//
// Uptime is read from State.Running.StartedAt rather than the termination's
// FinishedAt: a container that is up now and has been up longer than window
// proves by construction that whatever it terminated from is older than
// window, and it does so without depending on a timestamp the recovered
// state may not carry. A container that is not running, not ready, or whose
// start time is unknown is never treated as recovered — the gate only ever
// suppresses an alert on positive evidence of health.
func containerRecovered(cs corev1.ContainerStatus, now time.Time, window time.Duration) bool {
	running := cs.State.Running
	if running == nil || !cs.Ready || running.StartedAt.IsZero() {
		return false
	}
	return now.Sub(running.StartedAt.Time) >= window
}

// detectPodAlert inspects one pod's container statuses and returns the single
// worst bad state found, if any. Pure and unit-tested against fixture pod
// statuses (no cluster needed). Pods without the dada.io/app label are not
// console-managed apps and are skipped. OOMKilled takes priority over
// CrashLoopBackOff when both are present: it is the actual root cause, the
// backoff state is just its symptom. reasonError is checked last, after
// OOMKilled and the named waiting reasons, and only fires on a container that
// has already restarted at least once (RestartCount >= 1) so a first attempt
// still mid-flight is not misread as crashing.
//
// The two branches that read the historical LastTerminationState are gated by
// containerRecovered: a crash the container has demonstrably recovered from
// is history, not an incident. The CrashLoopBackOff/ImagePull branches read
// the container's current Waiting state and need no such gate — a container
// sitting in backoff has not recovered by definition.
func detectPodAlert(pod *corev1.Pod) (appHealthAlert, bool) {
	return detectPodAlertAt(pod, time.Now(), appHealthRecoveryWindow)
}

func detectPodAlertAt(pod *corev1.Pod, now time.Time, window time.Duration) (appHealthAlert, bool) {
	appName := pod.Labels["dada.io/app"]
	if appName == "" {
		return appHealthAlert{}, false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.LastTerminationState.Terminated != nil && cs.LastTerminationState.Terminated.Reason == reasonOOMKilled {
			if containerRecovered(cs, now, window) {
				continue
			}
			return appHealthAlert{
				Namespace: pod.Namespace,
				AppName:   appName,
				PodName:   pod.Name,
				Container: cs.Name,
				Reason:    reasonOOMKilled,
			}, true
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting == nil {
			continue
		}
		switch cs.State.Waiting.Reason {
		case reasonCrashLoopBackOff, reasonImagePullBackOff, reasonErrImagePull:
			return appHealthAlert{
				Namespace: pod.Namespace,
				AppName:   appName,
				PodName:   pod.Name,
				Container: cs.Name,
				Reason:    cs.State.Waiting.Reason,
			}, true
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		term := cs.LastTerminationState.Terminated
		if term == nil {
			term = cs.State.Terminated
		}
		if term == nil || term.ExitCode == 0 {
			continue
		}
		if cs.RestartCount < 1 {
			continue
		}
		if containerRecovered(cs, now, window) {
			continue
		}
		return appHealthAlert{
			Namespace: pod.Namespace,
			AppName:   appName,
			PodName:   pod.Name,
			Container: cs.Name,
			Reason:    reasonError,
			ExitCode:  term.ExitCode,
		}, true
	}
	return appHealthAlert{}, false
}

// claimAppHealthAlertSlot atomically claims the right to send one alert for
// (namespace, app) by upserting app_health_alerts, succeeding only when no
// send is recorded within cooldown. The conditional upsert makes the claim
// race-free across replicas: of two concurrent claims, exactly one affects a
// row. Alerts for different containers of the same app collapse to the same
// (namespace, app_name) key and so to a single email. reason/detail are
// persisted alongside the cooldown timestamp (P1-ALERTS-IN-UI) so the console
// can read back "what was detected" without a live cluster scan.
func claimAppHealthAlertSlot(ctx context.Context, pool *pgxpool.Pool, namespace, appName, reason, detail string, cooldown time.Duration) bool {
	ct, err := pool.Exec(ctx,
		`INSERT INTO app_health_alerts (namespace, app_name, last_sent_at, reason, detail)
		 VALUES ($1, $2, now(), $3, $4)
		 ON CONFLICT (namespace, app_name) DO UPDATE SET last_sent_at = now(), reason = $3, detail = $4
		 WHERE app_health_alerts.last_sent_at <= now() - make_interval(secs => $5)`,
		namespace, appName, reason, detail, cooldown.Seconds())
	if err != nil {
		log.Printf("app-health: cooldown claim for %s/%s failed: %v", namespace, appName, err)
		return false
	}
	return ct.RowsAffected() > 0
}

// appHealthSendErrorMaxLen bounds how much of a send error's text is
// persisted, so a verbose SMTP client error never grows the row (or a log
// line derived from it) without limit. Counted in runes, not bytes: the
// relay this goes through returns Cyrillic error text, and a byte-index
// slice can land inside a multi-byte rune and hand Postgres invalid UTF-8,
// which fails the very UPDATE meant to record the failure.
const appHealthSendErrorMaxLen = 500

// recordAppHealthAlertSend writes the real outcome of one send attempt for
// (namespace, appName), on the same row claimAppHealthAlertSlot already
// created. It is the answer to "did this app owner actually get the email",
// which until this migration only existed as a log line — and the pod that
// wrote that log line to app bruzas.85's alerts had already rotated it away
// by the time anyone went looking (P1-LOUD-CONSOLE-NOBODY-OPENS).
//
// On success, last_sent_at is left exactly as claimAppHealthAlertSlot set it
// (now()): the 24h cooldown behaves as it always did.
//
// On failure, last_sent_at is rolled back by appHealthAlertRetryBackoff
// instead of being cleared or left at "just now". Clearing it would let the
// very next tick re-claim and re-send immediately — a retry storm on a
// flaky relay, the exact failure mode claiming before sending exists to
// avoid. Leaving it at "just now" burns the full 24h cooldown on an app that
// never got its owner notified, which is this ticket's root bug in a new
// shape. The partial rollback is the compromise: bounded backoff, not an
// immediate retry and not a lost day.
func recordAppHealthAlertSend(ctx context.Context, pool *pgxpool.Pool, namespace, appName, recipient string, sendErr error) {
	if sendErr == nil {
		_, err := pool.Exec(ctx,
			`UPDATE app_health_alerts
			 SET last_send_attempt_at = now(), last_recipient = $3,
			     last_send_ok = true, last_send_error = NULL, send_failures = 0
			 WHERE namespace = $1 AND app_name = $2`,
			namespace, appName, recipient)
		if err != nil {
			log.Printf("app-health: record send outcome for %s/%s failed: %v", namespace, appName, err)
		}
		return
	}

	errText := sendErr.Error()
	if runes := []rune(errText); len(runes) > appHealthSendErrorMaxLen {
		errText = string(runes[:appHealthSendErrorMaxLen])
	}
	_, err := pool.Exec(ctx,
		`UPDATE app_health_alerts
		 SET last_send_attempt_at = now(), last_recipient = $3,
		     last_send_ok = false, last_send_error = $4,
		     send_failures = send_failures + 1,
		     last_sent_at = now() - make_interval(secs => $5) + make_interval(secs => $6)
		 WHERE namespace = $1 AND app_name = $2`,
		namespace, appName, recipient, errText,
		appHealthAlertCooldown.Seconds(), appHealthAlertRetryBackoff.Seconds())
	if err != nil {
		log.Printf("app-health: record send outcome for %s/%s failed: %v", namespace, appName, err)
	}
}

// touchAppHealthAlertSeen unconditionally records "this bad state was
// observed right now", independent of the 24h email cooldown
// (P1-ALERTS-IN-UI-FRESHNESS). Without this, app_health_alerts only ever
// gets written once per cooldown window, so the console cannot tell "still
// crashing" from "crashed once 20 hours ago and has been fine since" — the
// exact false-positive the console must never show (owner: a wrong red
// banner is worse than no banner). last_seen_at is refreshed on every tick
// that still detects the bad state; loadAppAlerts then gates on last_seen_at
// freshness (minutes), not on last_sent_at (which only moves once per 24h).
//
// The INSERT path seeds last_sent_at with the epoch sentinel, never with
// now(): if this ran first and stamped last_sent_at = now(), the very next
// claimAppHealthAlertSlot call would see a "just sent" cooldown row and skip
// the first real email entirely. The epoch sentinel guarantees the following
// claim's WHERE last_sent_at <= now() - cooldown always passes on a brand
// new row. The ON CONFLICT path never touches last_sent_at at all — only
// last_seen_at/reason/detail/cause/cause_line — so an established cooldown
// is never reset by a touch.
//
// cause/cause_line/causeKind are the console-facing crash explanation
// (notify.ClassifyCrashCause / notify.ExtractCauseLine); pass "" for any of
// them when this tick did not recompute it (see maybeCauseRefresh) rather
// than dropping the column update — a plain overwrite with "" would erase a
// cause recorded on an earlier tick just because this tick chose not to
// re-fetch the log. NULLIF/COALESCE on the UPDATE side keep an existing cause
// exactly as long as no fresher one is available; the INSERT side just
// stores whatever came in, since a brand-new row has nothing to preserve.
func touchAppHealthAlertSeen(ctx context.Context, pool *pgxpool.Pool, namespace, appName, reason, detail, cause, causeLine, causeKind string) {
	_, err := pool.Exec(ctx,
		`INSERT INTO app_health_alerts (namespace, app_name, last_sent_at, last_seen_at, reason, detail, cause, cause_line, cause_kind)
		 VALUES ($1, $2, to_timestamp(0), now(), $3, $4, NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''))
		 ON CONFLICT (namespace, app_name) DO UPDATE SET
		     last_seen_at = now(), reason = $3, detail = $4,
		     cause = COALESCE(NULLIF($5, ''), app_health_alerts.cause),
		     cause_line = COALESCE(NULLIF($6, ''), app_health_alerts.cause_line),
		     cause_kind = COALESCE(NULLIF($7, ''), app_health_alerts.cause_kind)`,
		namespace, appName, reason, detail, cause, causeLine, causeKind)
	if err != nil {
		log.Printf("app-health: touch-seen for %s/%s failed: %v", namespace, appName, err)
	}
}

// currentAlertCauseState reads back the reason and whether a non-empty cause
// is already stored for (namespace, appName), so maybeCauseRefresh can decide
// whether this tick needs a fresh kube-API log read at all. Returns an error
// for both a genuine query failure and "no row yet" (pgx.ErrNoRows) — the
// caller treats either the same way, as "must refresh", which is correct for
// a first-ever detection too.
//
// A stored cause_line that notify.IsUnusableCauseLine rejects also reports
// hasCause=false, so a row poisoned by the older extractor (bare traceback
// header, P1-CAUSELINE-HEADER) is re-derived on the next tick instead of
// sticking for as long as the app keeps crashlooping with the same reason.
// Residual cost: an app whose fresh log yields no cause at all keeps its
// poisoned line and pays one extra GetLogs per tick until its reason changes
// or it stops failing — bounded to legacy rows, since the fixed extractor can
// no longer produce that value.
func currentAlertCauseState(ctx context.Context, pool *pgxpool.Pool, namespace, appName string) (reason string, hasCause bool, err error) {
	var causeLine string
	err = pool.QueryRow(ctx,
		`SELECT COALESCE(reason, ''), cause IS NOT NULL AND cause <> '', COALESCE(cause_line, '')
		 FROM app_health_alerts WHERE namespace = $1 AND app_name = $2`,
		namespace, appName).Scan(&reason, &hasCause, &causeLine)
	if err != nil {
		return "", false, err
	}
	if notify.IsUnusableCauseLine(causeLine) {
		return reason, false, nil
	}
	return reason, hasCause, nil
}

// maybeCauseRefresh decides whether this tick is worth a tailLog call. Every
// bad pod is checked once per appHealthWatchInterval (3m), and tailLog is a
// live kube API GetLogs call per bad pod per tick — paying that on every tick
// for an app stuck crashlooping for days would be pure waste once the cause
// is already known and has not changed. So the log is only actually read
// when there is no cause on record yet, or the detected reason changed since
// the last tick (e.g. CrashLoopBackOff flipping to OOMKilled genuinely needs
// a fresh read). ClassifyCrashCause and ExtractCauseLine are cheap pure
// string scans, so they always run together once the log is fetched — no
// reason to split that cost further. Returns ("", "", "", "") when skipping,
// and the caller (touchAppHealthAlertSeen) already treats "" as "keep
// whatever is stored".
func (w *appHealthWatcher) maybeCauseRefresh(ctx context.Context, alert appHealthAlert) (logExcerpt, cause, causeLine, causeKind string) {
	prevReason, hasCause, err := currentAlertCauseState(ctx, w.h.pool, alert.Namespace, alert.AppName)
	if err == nil && hasCause && prevReason == alert.Reason {
		return "", "", "", ""
	}
	logExcerpt = w.tailLog(ctx, alert)
	causeKind, cause = notify.ClassifyCrashCause(logExcerpt)
	causeLine = notify.ExtractCauseLine(logExcerpt)
	return logExcerpt, cause, causeLine, causeKind
}

// maybeNotify sends the owner alert for one detected bad-state app, gated by
// the per-app 24h cooldown persisted in app_health_alerts. The recipient is
// resolved BEFORE the cooldown is claimed (P1-ALERT-OWNERLESS-DROP: claiming
// first meant a project with no resolvable owner burned its 24h cooldown slot
// on a drop, muting real alerts even after ownership got fixed). The cooldown
// is still claimed before the actual send, so a slow/failing SMTP relay
// cannot cause a retry storm on the next tick; a genuine second crash within
// the window is deliberately not re-alerted (P1-2b spec: at most one email
// per app per 24h). The unconditional seen-touch runs first, ahead of both
// recipient resolution and the cooldown claim, so the console's "is this
// still happening" signal never depends on whether an email actually goes
// out this tick.
//
// The cause lookup (maybeCauseRefresh) also runs ahead of emailableReason
// and the cooldown claim, and unconditionally: reasonError never emails
// (emailableReason is false for it) and a cooldown-blocked crash never
// reaches claimAppHealthAlertSlot, but the console must still be able to
// show why either one is failing, not just that it is. Reusing
// maybeCauseRefresh's return here — instead of a second tailLog call further
// down for the email body — means an app that is both newly detected and
// emailable pays for exactly one kube-API log read per tick, not two.
//
// The claim itself only ever meant "slot taken", never "email delivered" —
// recordAppHealthAlertSend is what turns "did this app owner get notified"
// into a query instead of a log grep. It runs after every Send call, success
// or failure, and a failed send additionally gives back part of the
// cooldown (appHealthAlertRetryBackoff) rather than either the full 24h or
// an immediate retry.
func (w *appHealthWatcher) maybeNotify(ctx context.Context, projectID uuid.UUID, alert appHealthAlert) {
	detail := alert.PodName + "/" + alert.Container
	if alert.Reason == reasonError {
		detail = fmt.Sprintf("%s exit=%d", detail, alert.ExitCode)
	}

	logExcerpt, cause, causeLine, causeKind := w.maybeCauseRefresh(ctx, alert)
	touchAppHealthAlertSeen(ctx, w.h.pool, alert.Namespace, alert.AppName, alert.Reason, detail, cause, causeLine, causeKind)

	if !emailableReason(alert.Reason) {
		return
	}

	to, source := w.h.resolveAlertRecipient(ctx, projectID)
	if to == "" {
		to = w.h.auditNotifyEmail
		source = alertSourceOperator
	}
	if to == "" {
		log.Printf("app-health: no owner/member/org/operator recipient for project %s, dropping alert for app=%s reason=%s", projectID, alert.AppName, alert.Reason)
		return
	}

	if !claimAppHealthAlertSlot(ctx, w.h.pool, alert.Namespace, alert.AppName, alert.Reason, detail, appHealthAlertCooldown) {
		return
	}

	if logExcerpt == "" {
		logExcerpt = w.tailLog(ctx, alert)
	}
	consoleLink := fmt.Sprintf("%s/projects/%s/apps/%s", w.h.cfg.PublicBaseURL, projectID, alert.AppName)
	codeHint := cause
	if codeHint == "" {
		codeHint = notify.ClassifyCrashLog(logExcerpt)
	}
	agentURL := consoleLink + "#agent"
	subject, body := notify.ComposeAppAlert(alert.AppName, alert.Reason, alert.PodName, logExcerpt, consoleLink, codeHint, agentURL)
	if source == alertSourceOperator {
		log.Printf("app-health: WARN no reachable owner for project %s, falling back to operator for app=%s reason=%s", projectID, alert.AppName, alert.Reason)
		subject, body = notify.ComposeNoOwnerFallback(projectID.String(), w.h.projectDisplayName(ctx, projectID), subject, body)
	}
	if err := w.h.auditNotifier.Send(to, subject, body); err != nil {
		log.Printf("app-health: send to %s failed for app=%s: %v", to, alert.AppName, err)
		recordAppHealthAlertSend(ctx, w.h.pool, alert.Namespace, alert.AppName, to, err)
		w.h.recordNotifySend(ctx, projectID, "AppHealthAlert", alert.AppName, source, err)
		return
	}
	recordAppHealthAlertSend(ctx, w.h.pool, alert.Namespace, alert.AppName, to, nil)
	w.h.recordNotifySend(ctx, projectID, "AppHealthAlert", alert.AppName, source, nil)
	log.Printf("app-health: alerted %s (source=%s) for app=%s reason=%s pod=%s", to, source, alert.AppName, alert.Reason, alert.PodName)
}

// tailLog best-effort fetches the crashed container's last lines: the
// previous run's log for a container that already restarted (CrashLoopBackOff,
// OOMKilled), falling back to the current run's log when there is no
// previous one (e.g. a container waiting on ImagePullBackOff that never
// started). Any failure returns "" and the alert email is still sent without
// the excerpt — a log-fetch problem must never block the alert itself.
func (w *appHealthWatcher) tailLog(ctx context.Context, alert appHealthAlert) string {
	logCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	tail := int64(appHealthLogTailLines)
	if s := w.fetchLog(logCtx, alert, true, tail); s != "" {
		return s
	}
	return w.fetchLog(logCtx, alert, false, tail)
}

func (w *appHealthWatcher) fetchLog(ctx context.Context, alert appHealthAlert, previous bool, tail int64) string {
	stream, err := w.clientset.CoreV1().Pods(alert.Namespace).GetLogs(alert.PodName, &corev1.PodLogOptions{
		Container: alert.Container,
		Previous:  previous,
		TailLines: &tail,
	}).Stream(ctx)
	if err != nil {
		return ""
	}
	defer stream.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(stream); err != nil && buf.Len() == 0 {
		return ""
	}
	return buf.String()
}
