package worker

import (
	"context"
	"time"

	"github.com/dada-tuda/console/portainer-agent/internal/db"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// tenantRetryInterval is how long a failed tenant write waits before the next
// attempt. VMs that are already correct are never re-touched, so this only
// paces genuinely broken hosts (down, key rotated) instead of SSHing every tick.
const tenantRetryInterval = 30 * time.Minute

// tenantApplier writes PROM_TENANT onto one VM. Injected so the reconcile logic
// is testable without an SSH server.
type tenantApplier func(ctx context.Context, host, user, privateKeyPEM, tenant string) (string, error)

// reconcileFleetTenants backfills PROM_TENANT onto VMs enrolled before the
// bootstrap wrote it.
//
// The fleet edge stack sends "X-Scope-OrgID: ${PROM_TENANT}" on remote_write.
// An empty variable means an empty header, and multitenant Mimir rejects the
// whole request with 401 — a VM in that state ships no metrics at all while
// looking healthy. New VMs get the value at enroll; this pass is what fixes the
// ones already running. Manual-connect VMs are skipped: their SSH key belongs to
// the operator and is scrubbed after enroll, so the platform key cannot log in.
func (w *VMWatcher) reconcileFleetTenants(ctx context.Context) {
	if w.cfg.AgentSSHPrivateKey == "" || w.cfg.PrometheusRemoteWriteURL == "" {
		return
	}
	servers, err := db.ListReadyAppServers(ctx, w.pool)
	if err != nil {
		log.Warn().Err(err).Msg("fleet tenant: list ready app servers failed")
		return
	}
	for _, s := range tenantTargets(servers) {
		if !w.tenantAttemptDue(s.ID) {
			continue
		}
		out, err := w.applyTenant(ctx, *s.VMIP, "root", w.cfg.AgentSSHPrivateKey, s.ProjectID.String())
		if err != nil {
			log.Warn().Err(err).Str("server", s.Name).Str("vm_ip", *s.VMIP).
				Msg("fleet tenant: write failed (metrics stay 401 until fixed)")
			continue
		}
		w.markTenantApplied(s.ID)
		log.Info().Str("server", s.Name).Str("tenant", s.ProjectID.String()).Str("result", out).
			Msg("fleet tenant: PROM_TENANT ensured")
	}
}

// tenantTargets keeps the servers this pass can actually reach: a live IP and a
// platform-provisioned host. Manual-connect VMs are enrolled with the operator's
// own key, which is scrubbed after enroll, so the platform key cannot log in and
// their tenant has to be set by whoever owns the box.
func tenantTargets(servers []db.AppServerRow) []db.AppServerRow {
	var out []db.AppServerRow
	for _, s := range servers {
		if s.VMIP == nil || *s.VMIP == "" || s.Source == "manual" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// tenantAttemptDue reports whether this server should be contacted now: never
// again once applied, otherwise no more often than tenantRetryInterval.
func (w *VMWatcher) tenantAttemptDue(id uuid.UUID) bool {
	w.tenantMu.Lock()
	defer w.tenantMu.Unlock()
	if w.tenantApplied == nil {
		w.tenantApplied = map[uuid.UUID]bool{}
		w.tenantTried = map[uuid.UUID]time.Time{}
	}
	if w.tenantApplied[id] {
		return false
	}
	last, seen := w.tenantTried[id]
	if seen && w.now().Sub(last) < tenantRetryInterval {
		return false
	}
	w.tenantTried[id] = w.now()
	return true
}

func (w *VMWatcher) markTenantApplied(id uuid.UUID) {
	w.tenantMu.Lock()
	defer w.tenantMu.Unlock()
	w.tenantApplied[id] = true
}

func (w *VMWatcher) now() time.Time {
	if w.clock != nil {
		return w.clock()
	}
	return time.Now()
}
