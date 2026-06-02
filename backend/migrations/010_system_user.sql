-- 010_system_user.sql
-- A fixed-UUID, non-loginable "system" user used as actor_id for agent-initiated
-- operations (e.g. DeployStack enqueued by the gitops-agent when a compose
-- file is edited via the editor). The all-zero UUID is a stable, well-known id.
-- password_hash is intentionally non-bcrypt so the account can never log in.

INSERT INTO users (id, username, email, password_hash, display_name)
VALUES (
    '00000000-0000-0000-0000-000000000000',
    'system',
    'system@dada.local',
    '!disabled',
    'DADA System'
)
ON CONFLICT (id) DO NOTHING;
