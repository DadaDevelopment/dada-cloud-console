-- 138_env_vars_secret_ref.sql
-- Lets one env var point at a k8s Secret the console does not own.
--
-- Apps that were never created through the console (internal/prod/telemost-bot
-- and every hand-written app-of-apps entry) carry extraEnv items shaped as
-- valueFrom.secretKeyRef{name,key}, pointing at Secrets made by hand. The
-- console's model had no way to say that: env_vars could only hold a literal
-- value, so such an app could never be adopted -- rendering it from the console
-- would either drop those variables or force their plaintext into git.
--
-- A row with secret_ref_name/secret_ref_key set carries NO value: the reference
-- itself is the value, and the console never reads the Secret's contents.
ALTER TABLE env_vars ADD COLUMN IF NOT EXISTS secret_ref_name VARCHAR(253);
ALTER TABLE env_vars ADD COLUMN IF NOT EXISTS secret_ref_key  VARCHAR(253);
ALTER TABLE env_vars ALTER COLUMN value_encrypted DROP NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'env_vars_value_or_ref'
    ) THEN
        ALTER TABLE env_vars ADD CONSTRAINT env_vars_value_or_ref CHECK (
            (value_encrypted IS NOT NULL AND secret_ref_name IS NULL AND secret_ref_key IS NULL)
            OR
            (value_encrypted IS NULL AND secret_ref_name IS NOT NULL AND secret_ref_key IS NOT NULL)
        );
    END IF;
END $$;
