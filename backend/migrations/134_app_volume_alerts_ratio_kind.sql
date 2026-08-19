-- app_volume_alerts.ratio previously only ever meant "bytes used / bytes
-- capacity" because kubelet_volume_stats_used_bytes was the only signal the
-- watcher read. That hid an entire failure mode: fonbet-value's PVC sat at
-- inode_free=0 (kubelet_volume_stats_inodes_used == kubelet_volume_stats_inodes)
-- while its byte ratio read 0.73, so the watcher never fired and the app
-- crashlooped on ENOSPC for five days with a "healthy" disk in every metric
-- the platform actually looked at.
--
-- ratio_kind tags which dimension the stored ratio value is: 'bytes' (the
-- existing meaning) or 'inodes'. DEFAULT 'bytes' with NOT NULL is safe for a
-- rolling deploy: every INSERT written by a replica still running the old
-- binary omits this column entirely, so Postgres fills the default rather
-- than rejecting the row -- no old replica ever sends an explicit NULL here.
ALTER TABLE app_volume_alerts
    ADD COLUMN IF NOT EXISTS ratio_kind TEXT NOT NULL DEFAULT 'bytes';

DO $$
BEGIN
    ALTER TABLE app_volume_alerts DROP CONSTRAINT IF EXISTS app_volume_alerts_ratio_kind_check;
    ALTER TABLE app_volume_alerts ADD CONSTRAINT app_volume_alerts_ratio_kind_check
        CHECK (ratio_kind IN ('bytes', 'inodes'));
EXCEPTION
    WHEN insufficient_privilege THEN
        NULL;
END;
$$;
