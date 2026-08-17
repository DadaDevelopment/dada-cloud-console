-- Registration happens through two structurally different doors: the
-- classic email/password form (#kc-register-form, tracked client-side via
-- the kc_register_* Metrika goals on id.dada-tuda.ru) and Keycloak identity
-- brokering (Yandex today, google/github also configured but new-user
-- signup blocked on them -- see argo-infra yandex-idp.yaml). A brokered
-- signup never touches the registration form's DOM, so the Metrika funnel
-- is structurally blind to it -- and per that same file, Yandex has been
-- the ONLY open door since 2026-08-13. Without a column, "how many people
-- signed up and through what door" was unanswerable from Postgres.
--
-- signup_channel is 'password' for the classic form, or the Keycloak IdP
-- alias ('yandex', 'google', 'github', ...) for a brokered signup --
-- provider-agnostic by construction, not hardcoded to Yandex. Existing rows
-- predate this column and their door is not recoverable after the fact, so
-- they stay NULL rather than guessed into 'password'.
ALTER TABLE users ADD COLUMN signup_channel varchar(32);

-- signup_channel is appended AFTER account_kind, not inserted before it:
-- Postgres's CREATE OR REPLACE VIEW only allows adding columns at the end of
-- the output list -- inserting one earlier renames the existing column at
-- that position instead, which errors 42P16 ("cannot change name of view
-- column"). Migration 132 first shipped with signup_channel ahead of
-- account_kind and broke exactly this way, red in CI on every build since.
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
    END AS account_kind,
    u.signup_channel
FROM users u;

GRANT SELECT ON user_accounts TO dada;
