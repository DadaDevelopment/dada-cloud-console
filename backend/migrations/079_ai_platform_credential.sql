-- A shared platform key had nowhere to live.
--
-- 036 scoped every AI provider credential to a project: project_id NOT NULL,
-- UNIQUE (project_id, provider). That is exactly right for BYOK -- a customer's
-- OpenAI key belongs to the project that pays for it and dies with it.
--
-- It has no room for the other case. The gateway now serves free-tier tier
-- aliases (fast / medium / smart) that fail over across gemini, nvidia_nim,
-- groq and sambanova. Those run on keys the platform holds, not keys any
-- customer brought, and every project is meant to reach them without doing
-- anything first. Under 036 the only way to express that is to copy the same
-- secret into a row per project -- 30 rows today, and silently zero for the
-- 31st project created tomorrow, which is a trap, not a default.
--
-- project_id NULL now means "the platform's own key for this provider". The
-- lookup in AIGetProviderCredential prefers the project row and falls back to
-- the NULL row, so BYOK still wins wherever a customer has set one and nothing
-- about the existing rows changes.
--
-- The FK stays: NULL satisfies a foreign key, so a platform row simply never
-- cascades from a project delete. The partial index is required because
-- UNIQUE (project_id, provider) treats NULLs as distinct and would happily
-- accept a second, conflicting platform key for the same provider.
--
-- Console reads are deliberately left alone: list/upsert/delete all filter on
-- project_id = $1, so a platform row is invisible in the UI and no customer
-- can overwrite or delete the key that other projects depend on.
ALTER TABLE ai_provider_credentials
    ALTER COLUMN project_id DROP NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_provider_credentials_platform
    ON ai_provider_credentials (provider)
 WHERE project_id IS NULL;
