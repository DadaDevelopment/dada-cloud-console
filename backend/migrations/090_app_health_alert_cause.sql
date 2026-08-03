-- app_health_alerts already carries detail (pod/container), which tells the
-- owner WHERE something crashed but never WHY. The watcher already fetches
-- the crashed container's log tail and classifies it (notify.ClassifyCrashLog)
-- for the alert email, then throws both away: nothing lands in the row, so
-- the console has no way to show a cause even though the platform already
-- worked it out.
--
-- cause is the short human hint ClassifyCrashLog produces (e.g. "ошибка в
-- коде приложения (Python)"); cause_line is the single log line the
-- classification matched against, kept as the concrete evidence next to the
-- hint. Both are nullable and best-effort: a log-fetch failure or an
-- unrecognized crash signature leaves them NULL, same as detail already
-- tolerates missing pod info.
ALTER TABLE app_health_alerts
    ADD COLUMN IF NOT EXISTS cause      TEXT,
    ADD COLUMN IF NOT EXISTS cause_line TEXT;
