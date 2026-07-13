-- Fix findata console state: the single `profi-vm` compose App becomes 4
-- first-class per-service Applications (postgres/backend/frontend/nginx), so the
-- deployed console (gitops-agent f2cd5ea, which renders/reads per-service) shows
-- them separately with per-app logs/metrics/state (scoped by
-- com.docker.compose.service on the ALREADY-RUNNING containers).
--
-- SAFETY: this ONLY writes resource_snapshots. It does NOT deploy, does NOT touch
-- the running profi-vm Portainer stack, does NOT start a second postgres. Zero
-- data risk. The desired.compose blocks are the VERBATIM live service specs (+
-- the external volume mapping profi_pg_data -> compose_profi_pg_data), so a LATER
-- deploy/image-update reproduces the stack exactly.
--
-- Run against the console DB:
--   kubectl exec -i -n argocd-prod deploy/<db-capable-pod> -- \
--     psql "$CONSOLE_DATABASE_URL" -f - < tasks/findata-split-state.sql
-- (any psql client with the console DB connection; it resolves fin-core/vm env by name)

BEGIN;

WITH e AS (
  SELECT env.id AS eid, env.project_id AS pid
  FROM environments env JOIN projects p ON p.id = env.project_id
  WHERE p.name = 'fin-core' AND env.runtime = 'vm'
  ORDER BY env.created_at
  LIMIT 1
)
INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
SELECT e.pid, e.eid, 'App', v.name, 'Ready', v.summary::jsonb, now()
FROM e, (VALUES
  ('postgres', '{"runtime":"compose","status":"Ready","adopted_from":"profi-vm","desired":{"image":"mirror.gcr.io/library/postgres:16-alpine","compose":{"image":"mirror.gcr.io/library/postgres:16-alpine","restart":"unless-stopped","environment":{"POSTGRES_DB":"feedback","POSTGRES_USER":"postgres","POSTGRES_PASSWORD":"pswd"},"ports":["65433:5432"],"volumes":["profi_pg_data:/var/lib/postgresql/data"]},"stack_volumes":{"profi_pg_data":{"external":true,"name":"compose_profi_pg_data"}}}}'),
  ('backend',  '{"runtime":"compose","status":"Ready","adopted_from":"profi-vm","desired":{"image":"nexus.dada-tuda.ru/dada/profi-backend:master-1.0.0-194","compose":{"image":"nexus.dada-tuda.ru/dada/profi-backend:master-1.0.0-194","restart":"unless-stopped","env_file":[".env"],"environment":{"DB_URL":"postgresql+asyncpg://postgres:pswd@postgres:5432/feedback"},"expose":["8001"],"depends_on":["postgres"]},"stack_volumes":{"profi_pg_data":{"external":true,"name":"compose_profi_pg_data"}}}}'),
  ('frontend', '{"runtime":"compose","status":"Ready","adopted_from":"profi-vm","desired":{"image":"nexus.dada-tuda.ru/dada/profi:master-1.0.0-174","compose":{"image":"nexus.dada-tuda.ru/dada/profi:master-1.0.0-174","restart":"unless-stopped","environment":{"VITE_API_BASE":"https://fin-data.pro"},"expose":["5173"]},"stack_volumes":{"profi_pg_data":{"external":true,"name":"compose_profi_pg_data"}}}}'),
  ('nginx',    '{"runtime":"compose","status":"Ready","adopted_from":"profi-vm","desired":{"image":"mirror.gcr.io/library/nginx:1.27-alpine","compose":{"image":"mirror.gcr.io/library/nginx:1.27-alpine","restart":"unless-stopped","depends_on":["backend","frontend"],"environment":{"DOMAIN":"fin-data.pro","NGINX_SSL_CERT_PATH":"/etc/nginx/certs/live/fin-data.pro/fullchain.pem","NGINX_SSL_KEY_PATH":"/etc/nginx/certs/live/fin-data.pro/privkey.pem","BACKEND_UPSTREAM":"backend:8001","FRONTEND_UPSTREAM":"frontend:5173"},"ports":["80:80","443:443"],"volumes":["/home/ubuntuuser/compose/nginx/default.conf.template:/etc/nginx/templates/default.conf.template:ro","/home/ubuntuuser/compose/nginx/.htpasswd:/etc/nginx/.htpasswd:ro","/etc/letsencrypt:/etc/nginx/certs:ro"]},"stack_volumes":{"profi_pg_data":{"external":true,"name":"compose_profi_pg_data"}}}}')
) AS v(name, summary)
ON CONFLICT (project_id, environment_id, kind, name)
DO UPDATE SET summary_json = EXCLUDED.summary_json, phase = EXCLUDED.phase, last_synced_at = now();

DELETE FROM resource_snapshots rs
USING (
  SELECT env.id AS eid, env.project_id AS pid
  FROM environments env JOIN projects p ON p.id = env.project_id
  WHERE p.name = 'fin-core' AND env.runtime = 'vm'
  ORDER BY env.created_at LIMIT 1
) e
WHERE rs.project_id = e.pid AND rs.environment_id = e.eid
  AND rs.kind = 'App' AND rs.name = 'profi-vm';

COMMIT;

-- verify: expect 4 rows (postgres/backend/frontend/nginx), no profi-vm
SELECT rs.name, rs.phase, rs.summary_json->>'adopted_from' AS adopted_from
FROM resource_snapshots rs
WHERE rs.kind = 'App' AND rs.environment_id = (
  SELECT env.id FROM environments env JOIN projects p ON p.id = env.project_id
  WHERE p.name = 'fin-core' AND env.runtime = 'vm' ORDER BY env.created_at LIMIT 1
)
ORDER BY rs.name;
