-- The platform is not a test probe.
--
-- 075 sorted every users row into synthetic/internal/customer by email
-- convention. The seeded "system" actor (010_system_user.sql, the all-zero uuid,
-- system@dada.local) fell into 'synthetic' by its @dada.local address, which put
-- it in the same bucket as our e2e probes and the seed rows.
--
-- That is fine for headline counts -- nobody counts synthetic either way -- but
-- it is wrong for path analysis. The audit trail now carries platform-performed
-- work under that actor (autoscale refusals, box suspend/resume/up/delete,
-- deploy hooks), and "what did the platform do to this app" is a question a
-- support read has to be able to ask without also dragging in whatever
-- dada-e2e-test@ was doing that hour.
--
-- Fourth kind, matched on the id rather than the address so a future rename of
-- the row cannot silently move it back:
--   platform -- the system actor: work the platform did on its own
CREATE OR REPLACE VIEW user_accounts AS
SELECT
    u.id,
    u.email,
    u.username,
    u.created_at,
    CASE
        WHEN u.id = '00000000-0000-0000-0000-000000000000'::uuid THEN 'platform'
        WHEN u.username LIKE 'service-account-%'    THEN 'synthetic'
        WHEN u.email    LIKE '%@keycloak.local'     THEN 'synthetic'
        WHEN u.email    LIKE '%@dada.local'         THEN 'synthetic'
        WHEN u.email    LIKE 'dada-e2e-test@%'      THEN 'synthetic'
        WHEN u.email    LIKE 'a5-testuser-%'        THEN 'synthetic'
        WHEN u.email    LIKE 'sp2verify%'           THEN 'synthetic'
        WHEN u.email    LIKE '%@sp2-verify.%'       THEN 'synthetic'
        WHEN u.email    LIKE '%@dada-tuda.ru'       THEN 'internal'
        ELSE 'customer'
    END AS account_kind
FROM users u;

GRANT SELECT ON user_accounts TO dada;
