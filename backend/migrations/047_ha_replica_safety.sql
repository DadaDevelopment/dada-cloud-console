CREATE TABLE IF NOT EXISTS app_health_alerts (
    namespace    TEXT        NOT NULL,
    app_name     TEXT        NOT NULL,
    last_sent_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, app_name)
);

DELETE FROM domain_hostnames dh
 USING domain_hostnames keep
 WHERE dh.managed AND keep.managed
   AND dh.environment_id = keep.environment_id
   AND dh.app_name = keep.app_name
   AND dh.id <> keep.id
   AND dh.status = 'pending'
   AND (keep.created_at < dh.created_at
        OR (keep.created_at = dh.created_at AND keep.id < dh.id));

CREATE UNIQUE INDEX IF NOT EXISTS uniq_domain_hostnames_managed_env_app
    ON domain_hostnames (environment_id, app_name)
 WHERE managed;
