-- 035_domain_hostname_reissue.sql
-- Tracks the last time ReconcilePendingHostnames re-issued the DNS write for a
-- managed (surrogate) hostname stuck unresolved past the DNS-stuck window. Used
-- as a per-row cooldown so a hostname that stays stuck cannot be re-issued on
-- every 1-minute reconcile tick, which would itself add load to the very Beget
-- API path whose overload caused the write to be lost in the first place.

ALTER TABLE domain_hostnames
    ADD COLUMN IF NOT EXISTS last_reissue_at TIMESTAMPTZ;
