-- 018_build_artifacts.sql
-- Mobile delivery (ADR-010): Android builds produce APK/AAB pushed to the Nexus
-- raw repo by Jenkins. The control plane records one row per artifact after
-- confirming it exists in Nexus (HEAD). The backend serves downloads by
-- proxying nexus_url with server-side Nexus creds (never exposes them).
-- Forward-only, additive, idempotent.

CREATE TABLE IF NOT EXISTS build_artifacts (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    build_id      UUID         NOT NULL REFERENCES builds(id) ON DELETE CASCADE,
    type          VARCHAR(10)  NOT NULL CHECK (type IN ('apk', 'aab')),
    nexus_url     VARCHAR(1000) NOT NULL,                 -- absolute raw-repo URL
    size          BIGINT       NOT NULL DEFAULT 0,        -- bytes (Content-Length)
    version_code  INTEGER,                                -- Android versionCode
    sha256        VARCHAR(64),                            -- from console marker
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE(build_id, type)
);

CREATE INDEX IF NOT EXISTS idx_build_artifacts_build_id
    ON build_artifacts(build_id);
