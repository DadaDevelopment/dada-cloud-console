package api

import (
	"context"
	"fmt"
	"time"

	"github.com/dada-tuda/console/backend/internal/grafana"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Alert-rule self-healing (ADR-011). Shared Grafana runs on emptyDir (longhorn
// can't schedule a PVC — over-provisioned), so every Grafana pod restart wipes
// all API-provisioned folders/dashboards/alert rules while the backend DB still
// holds them. Alerting then silently stops until each rule is manually
// re-touched. This reconciler re-asserts the backend's alert rules into Grafana
// so they self-heal. Folders are re-created as a prerequisite; dashboards
// already re-create idempotently on access (ensureGrafanaResource).
//
// Contact points are NOT reconciled: their secrets (bot tokens, email
// addresses, webhook URLs) are never persisted backend side (see CreateChannel),
// so a wiped contact point cannot be rebuilt. A rule whose channel is gone is
// re-created without routing (fires but unrouted) rather than dropped.

const defaultReconcileInterval = 5 * time.Minute

// desiredAlertRule is the backend's view of one alert rule, sufficient to
// re-provision it into Grafana. Built from the Postgres mirror; carries no DB
// handle so the per-rule reconcile is unit-testable against a fake Grafana.
type desiredAlertRule struct {
	uid          string // stable Grafana rule uid
	title        string
	folderUID    string
	folderTitle  string
	ruleGroup    string
	expr         string
	condition    string
	threshold    float64
	forDur       string
	contactPoint string // receiver name, or "" for unrouted
	labels       map[string]string
}

// StartGrafanaReconciler runs an initial reconcile pass, then re-asserts the
// backend's alert rules into Grafana on every tick until ctx is cancelled.
// No-op when Grafana is unconfigured (grafana client nil). interval <= 0 falls
// back to the default.
func (h *Handler) StartGrafanaReconciler(ctx context.Context, interval time.Duration) {
	if h.grafana == nil {
		return
	}
	if interval <= 0 {
		interval = defaultReconcileInterval
	}
	go func() {
		// Boot pass: re-create anything a restart wiped before the first tick.
		if n, err := h.reconcileAlertRules(ctx); err != nil {
			log.Warn().Err(err).Msg("grafana alert-rule reconcile (boot) failed")
		} else if n > 0 {
			log.Info().Int("recreated", n).Msg("grafana alert-rule reconcile (boot) re-asserted rules")
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n, err := h.reconcileAlertRules(ctx); err != nil {
					log.Warn().Err(err).Msg("grafana alert-rule reconcile failed")
				} else if n > 0 {
					log.Info().Int("recreated", n).Msg("grafana alert-rule reconcile re-asserted rules")
				}
			}
		}
	}()
}

// reconcileAlertRules loads every enabled alert rule from the mirror, checks each
// against Grafana, and re-provisions the ones that are missing. Returns the count
// re-created. Safe to call concurrently with the create/delete handlers: existence
// is checked per rule and creation is keyed by the stable uid.
func (h *Handler) reconcileAlertRules(ctx context.Context) (int, error) {
	if h.grafana == nil {
		return 0, nil
	}
	desired, err := h.loadDesiredAlertRules(ctx)
	if err != nil {
		return 0, err
	}
	recreated := 0
	for _, d := range desired {
		ok, err := reconcileDesiredRule(ctx, h.grafana, h.cfg.GrafanaPromDatasourceUID, d)
		if err != nil {
			// One bad rule must not stall the rest; log and continue.
			log.Warn().Err(err).Str("uid", d.uid).Str("title", d.title).
				Msg("grafana alert-rule reconcile: rule failed")
			continue
		}
		if ok {
			recreated++
		}
	}
	return recreated, nil
}

// loadDesiredAlertRules builds the desired-state list from the Postgres mirror,
// joining the resource (folder uid + project) and project owner (org label).
func (h *Handler) loadDesiredAlertRules(ctx context.Context) ([]desiredAlertRule, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT r.id, r.name, r.metric, r.condition, r.threshold, r.duration,
		        r.channel_id, COALESCE(r.grafana_rule_uid, ''),
		        a.id, a.project_id, COALESCE(a.grafana_folder_uid, ''),
		        p.owner_id
		   FROM monitoring_alert_rules r
		   JOIN monitoring_apps a ON a.id = r.monitoring_app_id
		   JOIN projects p ON p.id = a.project_id
		  WHERE r.enabled = true`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []desiredAlertRule{}
	for rows.Next() {
		var (
			ruleID    uuid.UUID
			name      string
			metric    string
			condition string
			threshold float64
			duration  string
			channelID *uuid.UUID
			ruleUID   string
			appID     uuid.UUID
			projectID uuid.UUID
			folderUID string
			ownerID   *uuid.UUID
		)
		if err := rows.Scan(&ruleID, &name, &metric, &condition, &threshold, &duration,
			&channelID, &ruleUID, &appID, &projectID, &folderUID, &ownerID); err != nil {
			return nil, err
		}

		if ruleUID == "" {
			ruleUID = ruleUIDForID(ruleID)
		}
		if folderUID == "" {
			folderUID = folderUIDForProject(projectID)
		}
		if duration == "" {
			duration = "5m"
		}
		orgID := ""
		if ownerID != nil {
			orgID = ownerID.String()
		}
		labels := monitoringLabels(orgID, &models.MonitoringApp{ID: appID, ProjectID: projectID})
		contactPoint := ""
		if channelID != nil {
			contactPoint = receiverName(*channelID)
		}

		out = append(out, desiredAlertRule{
			uid:          ruleUID,
			title:        name,
			folderUID:    folderUID,
			folderTitle:  "Project " + projectID.String(),
			ruleGroup:    appID.String(),
			expr:         fmt.Sprintf("avg(%s%s)", metric, promSelector(labels)),
			condition:    condition,
			threshold:    threshold,
			forDur:       duration,
			contactPoint: contactPoint,
			labels:       labels,
		})
	}
	return out, rows.Err()
}

// reconcileDesiredRule re-provisions one rule into Grafana iff it is missing.
// Returns true when it re-created the rule. Pure of DB access so it can be
// unit-tested against an httptest Grafana.
func reconcileDesiredRule(ctx context.Context, gc *grafana.Client, promDSUID string, d desiredAlertRule) (bool, error) {
	exists, err := gc.AlertRuleExists(ctx, d.uid)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	// Folder is a prerequisite for the rule (it references FolderUID). Idempotent.
	if err := gc.EnsureFolder(ctx, d.folderUID, d.folderTitle); err != nil {
		return false, err
	}

	// A wiped contact point can't be rebuilt (secrets not persisted). If the
	// rule's channel is gone, re-create the rule unrouted rather than fail.
	contactPoint := d.contactPoint
	if contactPoint != "" {
		ok, err := gc.ContactPointExists(ctx, contactPoint)
		if err != nil {
			return false, err
		}
		if !ok {
			log.Warn().Str("uid", d.uid).Str("receiver", contactPoint).
				Msg("grafana alert-rule reconcile: contact point missing, re-creating rule unrouted")
			contactPoint = ""
		}
	}

	rule := grafana.BuildThresholdRule(promDSUID, grafana.ThresholdRule{
		UID:          d.uid,
		Title:        d.title,
		FolderUID:    d.folderUID,
		RuleGroup:    d.ruleGroup,
		Expr:         d.expr,
		Condition:    d.condition,
		Threshold:    d.threshold,
		For:          d.forDur,
		ContactPoint: contactPoint,
		Labels:       d.labels,
	})
	if _, err := gc.CreateAlertRule(ctx, rule); err != nil {
		return false, err
	}
	return true, nil
}
