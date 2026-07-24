-- 045_build_fail_reason.sql
-- First-class build failure reason: a stable machine code (no_dockerfile,
-- dockerfile_build_failed, ...) alongside the existing free-text
-- error_message, so the console can render an actionable hint instead of a
-- raw Jenkins error string.

ALTER TABLE builds ADD COLUMN IF NOT EXISTS fail_reason TEXT;
