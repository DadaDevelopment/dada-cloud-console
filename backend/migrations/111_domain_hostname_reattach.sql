-- 111_domain_hostname_reattach.sql
-- Support columns for ReattachOrphanedHostnames: re-driving a failed
-- domain_hostnames row whose Ingress was lost out-of-band (e.g. DeleteApp
-- removing a gitops-repo directory that CreateApp under the same name never
-- fully re-rendered), while a live App resource_snapshot still claims the
-- hostname.
--
-- attach_started_at decouples the 48h attach-window clock
-- (hostnamePendingExpired) from created_at. Without this, a row re-driven
-- from 'failed' back to 'pending' keeps its original (possibly month-old)
-- created_at, so the very next ReconcilePendingHostnames tick sees the
-- window already blown and fails it again -- an infinite failed/pending
-- flap. Backfilled to created_at for every existing row so nothing changes
-- for hostnames the reattach pass never touches; ReattachOrphanedHostnames
-- resets it to now() on every re-drive.
--
-- reattach_count caps how many times a single row may be re-driven. The
-- reattach pass exists because a config error (an orphaned domain) can
-- recur for reasons outside its control -- e.g. the app itself flips back
-- to worker/portless between ticks -- so an unbounded retry loop is exactly
-- the kind of self-feeding storm this project has hit before (see the
-- default-domain DNS reissue cooldown next to it). Three tries, spaced by
-- the cooldown below, is enough to ride out a transient snapshot-sync gap
-- without hammering gitops forever.

ALTER TABLE domain_hostnames
    ADD COLUMN IF NOT EXISTS attach_started_at TIMESTAMPTZ;

UPDATE domain_hostnames
   SET attach_started_at = created_at
 WHERE attach_started_at IS NULL;

ALTER TABLE domain_hostnames
    ADD COLUMN IF NOT EXISTS reattach_count INT NOT NULL DEFAULT 0;
