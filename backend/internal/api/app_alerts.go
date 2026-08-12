package api

import (
	"context"
	"time"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
)

// appAlertRow is one cooldown-table row read back for the console, already
// tagged with its alert type ("crash" or "volume") and app name so it can be
// grouped independently of which table it came from.
type appAlertRow struct {
	AppName    string
	Type       string
	Reason     string
	Detail     string
	Cause      string
	CauseLine  string
	CauseKind  string
	Ratio      *float64
	DetectedAt time.Time
}

// groupAppAlerts turns the flat cooldown rows into a map keyed by app name,
// each value being that app's alerts sorted newest-first. Pure and unit
// tested without a database: the SQL side is just a plain SELECT, all the
// shaping logic worth getting wrong lives here.
func groupAppAlerts(rows []appAlertRow) map[string][]models.AppAlert {
	out := map[string][]models.AppAlert{}
	for _, r := range rows {
		out[r.AppName] = append(out[r.AppName], models.AppAlert{
			Type:       r.Type,
			Reason:     r.Reason,
			Detail:     r.Detail,
			Cause:      r.Cause,
			CauseLine:  r.CauseLine,
			CauseKind:  r.CauseKind,
			Ratio:      r.Ratio,
			DetectedAt: r.DetectedAt,
		})
	}
	for name := range out {
		alerts := out[name]
		for i := 1; i < len(alerts); i++ {
			j := i
			for j > 0 && alerts[j].DetectedAt.After(alerts[j-1].DetectedAt) {
				alerts[j], alerts[j-1] = alerts[j-1], alerts[j]
				j--
			}
		}
		out[name] = alerts
	}
	return out
}

// appAlertTypeURL tags an alert raised by the URL-reality watcher, i.e. the app
// is live but does not answer HTTP on its own in-cluster Service. Named because
// RestateUnreachablePhase keys the phase demotion off it and the two must not
// drift apart.
const appAlertTypeURL = "url"

// applyAppAlerts stamps each app's Alerts field from the grouped map, by
// name. Apps with no matching alerts are left with a nil (omitted) slice.
func applyAppAlerts(apps []models.ResourceSnapshot, byApp map[string][]models.AppAlert) {
	for i := range apps {
		if a, ok := byApp[apps[i].Name]; ok {
			apps[i].Alerts = a
		}
	}
}

// loadAppAlerts reads the freshly-seen cooldown rows for namespace out of
// app_health_alerts, app_volume_alerts and app_url_alerts and groups them by
// app name, ready to stamp onto ListApps' result. This is the one extra
// query ListApps pays for the P1-ALERTS-IN-UI feature: a single indexed
// lookup per table, no k8s or Prometheus access, keeping the endpoint inside
// its latency budget. Any query failure is logged by the caller's err return
// and treated as "no alerts" rather than failing the whole app list.
//
// Freshness is judged on COALESCE(last_seen_at, last_sent_at), never on
// last_sent_at alone (P1-ALERTS-IN-UI-FRESHNESS): last_sent_at only moves
// once per 24h cooldown, so gating on it would keep showing a red banner for
// a day after the app was actually fixed — exactly the false-positive the
// owner ruled out ("wrong alert is worse than no alert"). last_seen_at is
// touched on every tick the watcher still detects the bad state (see
// touchAppHealthAlertSeen/touchAppVolumeAlertSeen/recordURLProbeFailure), so
// it reflects "still happening right now" instead. The COALESCE falls back
// to last_sent_at for rows written before this migration/deploy landed,
// where last_seen_at is still NULL. app_url_alerts never sets last_sent_at
// at all (this watcher sends no email), so its COALESCE always resolves to
// last_seen_at; the column is kept only so all three queries share this one
// shape.
//
// app_url_alerts additionally gates on consecutive_failures reaching
// appURLAlertFailureThreshold: the row exists (and its counter climbs) from
// the very first failing probe, but the console must only show a banner once
// the anti-flap threshold is actually crossed, same as the in-memory check
// app_url_watcher.go's recordProbeResult performs before logging.
func (h *Handler) loadAppAlerts(ctx context.Context, namespace string) (map[string][]models.AppAlert, error) {
	var rows []appAlertRow

	hrows, err := h.pool.Query(ctx,
		`SELECT app_name, COALESCE(reason, ''), COALESCE(detail, ''), COALESCE(cause, ''), COALESCE(cause_line, ''), COALESCE(cause_kind, ''), COALESCE(last_seen_at, last_sent_at)
		 FROM app_health_alerts
		 WHERE namespace = $1 AND COALESCE(last_seen_at, last_sent_at) > now() - make_interval(secs => $2)`,
		namespace, appHealthAlertFreshWindow.Seconds())
	if err != nil {
		return nil, err
	}
	for hrows.Next() {
		var r appAlertRow
		if scanErr := hrows.Scan(&r.AppName, &r.Reason, &r.Detail, &r.Cause, &r.CauseLine, &r.CauseKind, &r.DetectedAt); scanErr != nil {
			hrows.Close()
			return nil, scanErr
		}
		r.Type = "crash"
		rows = append(rows, r)
	}
	hrows.Close()
	if err := hrows.Err(); err != nil {
		return nil, err
	}

	vrows, err := h.pool.Query(ctx,
		`SELECT app_name, ratio, COALESCE(last_seen_at, last_sent_at)
		 FROM app_volume_alerts
		 WHERE namespace = $1 AND COALESCE(last_seen_at, last_sent_at) > now() - make_interval(secs => $2)`,
		namespace, appVolumeAlertFreshWindow.Seconds())
	if err != nil {
		return nil, err
	}
	for vrows.Next() {
		var r appAlertRow
		var ratio *float64
		if scanErr := vrows.Scan(&r.AppName, &ratio, &r.DetectedAt); scanErr != nil {
			vrows.Close()
			return nil, scanErr
		}
		r.Type = "volume"
		r.Ratio = ratio
		rows = append(rows, r)
	}
	vrows.Close()
	if err := vrows.Err(); err != nil {
		return nil, err
	}

	urows, err := h.pool.Query(ctx,
		`SELECT app_name, COALESCE(reason, ''), COALESCE(detail, ''), COALESCE(last_seen_at, last_sent_at)
		 FROM app_url_alerts
		 WHERE namespace = $1 AND consecutive_failures >= $2
		   AND COALESCE(last_seen_at, last_sent_at) > now() - make_interval(secs => $3)`,
		namespace, appURLAlertFailureThreshold, appURLAlertFreshWindow.Seconds())
	if err != nil {
		return nil, err
	}
	for urows.Next() {
		var r appAlertRow
		if scanErr := urows.Scan(&r.AppName, &r.Reason, &r.Detail, &r.DetectedAt); scanErr != nil {
			urows.Close()
			return nil, scanErr
		}
		r.Type = appAlertTypeURL
		rows = append(rows, r)
	}
	urows.Close()
	if err := urows.Err(); err != nil {
		return nil, err
	}

	return groupAppAlerts(rows), nil
}

// environmentNamespace resolves an environment's k8s namespace, used by
// ListApps to scope the alert lookup. Returns "" (and the caller skips alert
// enrichment) for a compose/VM environment or any lookup failure.
func (h *Handler) environmentNamespace(ctx context.Context, envID uuid.UUID) string {
	var ns string
	if err := h.pool.QueryRow(ctx,
		`SELECT namespace FROM environments WHERE id = $1`, envID).Scan(&ns); err != nil {
		return ""
	}
	return ns
}
