-- app_health_alerts.cause (090_app_health_alert_cause.sql) already carries the
-- human-readable hint, but nothing machine-readable says whether that hint
-- points at the app's own code or at OUR platform. That gap let a genuine
-- platform network failure (fonbet-value, 2026-08-12: "No route to host" to
-- pg-router) render as "ошибка в коде приложения (Python)", because the log
-- was a Python traceback and the Python signature table matched before
-- anyone asked what actually broke.
--
-- cause_kind is the machine-readable counterpart notify.ClassifyCrashCause
-- now returns alongside the human text: 'app_code', 'platform_network' or
-- 'platform_storage'. NULL means unknown, same tolerance as cause/cause_line
-- already have for an unrecognized signature or a failed log read.
ALTER TABLE app_health_alerts
    ADD COLUMN IF NOT EXISTS cause_kind TEXT;
