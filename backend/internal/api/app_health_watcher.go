package api

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/google/uuid"
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
)

// appHealthAlert is one detected bad-state container, ready to notify on.
type appHealthAlert struct {
	Namespace string
	AppName   string
	PodName   string
	Container string
	Reason    string
}

// dedupKey identifies one app for the 24h cooldown map; alerts for different
// containers of the same app collapse to a single email.
func (a appHealthAlert) dedupKey() string {
	return a.Namespace + "/" + a.AppName
}

// appHealthWatcher polls user-project namespaces for pods stuck in a bad
// state and emails the project owner once per app per cooldown window.
type appHealthWatcher struct {
	clientset kubernetes.Interface
	h         *Handler

	mu       sync.Mutex
	lastSent map[string]time.Time
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
	w := &appHealthWatcher{clientset: clientset, h: h, lastSent: map[string]time.Time{}}
	go func() {
		w.tick(ctx)
		t := time.NewTicker(appHealthWatchInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				w.tick(ctx)
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

// projectOwnerEmail resolves the recipient for an app-health alert: the
// project's owner, the same LEFT JOIN users ON owner_id pattern used across
// projects.go/admin_overview.go. Empty when the project has no owner_id
// (team-owned project with no single owner on record) so the caller can skip
// sending rather than guess a recipient.
func (h *Handler) projectOwnerEmail(ctx context.Context, projectID uuid.UUID) string {
	var email string
	err := h.pool.QueryRow(ctx,
		`SELECT u.email FROM projects p
		 JOIN users u ON u.id = p.owner_id
		 WHERE p.id = $1`, projectID).Scan(&email)
	if err != nil {
		return ""
	}
	return email
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

// detectPodAlert inspects one pod's container statuses and returns the single
// worst bad state found, if any. Pure and unit-tested against fixture pod
// statuses (no cluster needed). Pods without the dada.io/app label are not
// console-managed apps and are skipped. OOMKilled takes priority over
// CrashLoopBackOff when both are present: it is the actual root cause, the
// backoff state is just its symptom.
func detectPodAlert(pod *corev1.Pod) (appHealthAlert, bool) {
	appName := pod.Labels["dada.io/app"]
	if appName == "" {
		return appHealthAlert{}, false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.LastTerminationState.Terminated != nil && cs.LastTerminationState.Terminated.Reason == reasonOOMKilled {
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
	return appHealthAlert{}, false
}

// maybeNotify sends the owner alert for one detected bad-state app, gated by
// the per-app 24h cooldown. The cooldown timestamp is set before the send
// attempt so a slow/failing SMTP relay cannot cause a retry storm on the next
// tick; a genuine second crash within the window is deliberately not
// re-alerted (P1-2b spec: at most one email per app per 24h).
func (w *appHealthWatcher) maybeNotify(ctx context.Context, projectID uuid.UUID, alert appHealthAlert) {
	key := alert.dedupKey()

	w.mu.Lock()
	if last, ok := w.lastSent[key]; ok && time.Since(last) < appHealthAlertCooldown {
		w.mu.Unlock()
		return
	}
	w.lastSent[key] = time.Now()
	w.mu.Unlock()

	to := w.h.projectOwnerEmail(ctx, projectID)
	if to == "" {
		log.Printf("app-health: no owner email for project %s, dropping alert for app=%s reason=%s", projectID, alert.AppName, alert.Reason)
		return
	}

	logExcerpt := w.tailLog(ctx, alert)
	consoleLink := fmt.Sprintf("%s/projects/%s/apps/%s", w.h.cfg.PublicBaseURL, projectID, alert.AppName)
	subject, body := notify.ComposeAppAlert(alert.AppName, alert.Reason, alert.PodName, logExcerpt, consoleLink)
	if err := w.h.auditNotifier.Send(to, subject, body); err != nil {
		log.Printf("app-health: send to %s failed for app=%s: %v", to, alert.AppName, err)
		return
	}
	log.Printf("app-health: alerted %s for app=%s reason=%s pod=%s", to, alert.AppName, alert.Reason, alert.PodName)
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
