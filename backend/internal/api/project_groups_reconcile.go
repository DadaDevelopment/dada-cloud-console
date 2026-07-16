package api

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
)

const (
	projectGroupReconcileInterval = 5 * time.Minute
	projectGroupReconcileSettle   = 30 * time.Second
	projectGroupEnsureTimeout     = 30 * time.Second
)

// StartProjectGroupReconciler launches the background loop that heals drift
// between dada-cloud's project rows and user-service's Keycloak group tree.
//
// A project row is created in dada-cloud's DB independently of its IAM groups,
// which are provisioned out-of-band (ensureProjectGroupsAsync, fire-and-forget).
// When that provisioning never lands — user-service slow/down, a dropped call —
// the project exists and deploys yet its /orgs/{org}/projects/{id} subtree is
// missing, so member/role lookups 404 forever with nothing to re-drive them.
//
// This reconciler walks every project on a ticker and (idempotently) ensures its
// groups exist, so a project that missed provisioning self-heals within a cycle
// instead of staying broken until someone re-triggers a console action. No-op when
// group sync is disabled (h.usersvc nil), so tests and off-cluster dev never spawn it.
func (h *Handler) StartProjectGroupReconciler(ctx context.Context) {
	if h.usersvc == nil {
		return
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(projectGroupReconcileSettle):
		}
		h.reconcileProjectGroups(ctx)
		ticker := time.NewTicker(projectGroupReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.reconcileProjectGroups(ctx)
			}
		}
	}()
}

// reconcileProjectGroups ensures IAM groups for every project, one at a time.
//
// Sequential by design: user-service's createProject fans out ~20 Keycloak admin
// calls, so provisioning the whole backlog concurrently is exactly the stampede
// that wedged IAM before. Projects already confirmed (groupsEnsured) are skipped,
// so once the backlog drains this settles into a cheap per-cycle no-op. The full
// project set is read into memory before any EnsureProjectGroups call so the pooled
// connection is released rather than held across the whole reconcile.
func (h *Handler) reconcileProjectGroups(ctx context.Context) {
	type project struct {
		id, org, slug, display, ownerSub string
	}
	rows, err := h.pool.Query(ctx, `
		SELECT p.id, COALESCE(p.org_id, ''), p.name, COALESCE(p.display_name, ''),
		       COALESCE(u.keycloak_sub, '')
		  FROM projects p
		  LEFT JOIN users u ON u.id = p.owner_id
		 WHERE COALESCE(p.org_id, '') <> ''
		 ORDER BY p.created_at`)
	if err != nil {
		log.Printf("iam reconcile: list projects: %v", err)
		return
	}
	var projects []project
	for rows.Next() {
		var id uuid.UUID
		var p project
		if err := rows.Scan(&id, &p.org, &p.slug, &p.display, &p.ownerSub); err != nil {
			log.Printf("iam reconcile: scan project: %v", err)
			continue
		}
		p.id = id.String()
		projects = append(projects, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("iam reconcile: iterate projects: %v", err)
		return
	}

	var healed int
	for _, p := range projects {
		if ctx.Err() != nil {
			return
		}
		if _, done := h.groupsEnsured.Load(p.id); done {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, projectGroupEnsureTimeout)
		err := h.usersvc.EnsureProjectGroups(cctx, p.org, p.id, p.slug, p.display, p.ownerSub)
		cancel()
		if err != nil {
			log.Printf("iam reconcile: ensure project=%s org=%s: %v", p.id, p.org, err)
			continue
		}
		h.groupsEnsured.Store(p.id, struct{}{})
		healed++
	}
	if healed > 0 {
		log.Printf("iam reconcile: provisioned groups for %d project(s)", healed)
	}
}
