CREATE TABLE payment_oauth_states (
  state       TEXT PRIMARY KEY,
  project_id  UUID NOT NULL,
  environment_id UUID NOT NULL,
  app_name    TEXT NOT NULL,
  user_sub    TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE payment_connections (
  id               UUID PRIMARY KEY,
  project_id       UUID NOT NULL,
  environment_id   UUID NOT NULL,
  app_name         TEXT NOT NULL,
  account_id       TEXT,
  me_raw           JSONB,
  access_token_enc TEXT NOT NULL,
  expires_at       TIMESTAMPTZ,
  status           TEXT NOT NULL DEFAULT 'active',
  webhook_ids      JSONB NOT NULL DEFAULT '[]',
  webhook_note     TEXT,
  connected_by_sub TEXT NOT NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (environment_id, app_name)
);

CREATE INDEX IF NOT EXISTS idx_payment_connections_project
  ON payment_connections (project_id);
