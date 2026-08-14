package api

import (
	"context"

	"github.com/dada-tuda/console/backend/internal/crypto"
)

// appEnvVarsByNamespace decrypts and returns every env_vars row for the app
// named appName running in namespace, keyed by env var key.
//
// appHealthWatcher only ever has a namespace and an app name in hand (see
// appHealthAlert), never an environment UUID, so this joins env_vars against
// environments on namespace rather than taking an environment_id the caller
// does not have. There is no existing helper that reads env vars this way --
// ListEnvVars (envvars.go) takes an environment_id + selectively decrypts
// only non-secret values for display, which is the wrong shape here on both
// counts: a bad DATABASE_URL is normally marked secret, and the classifier
// needs its plaintext to compare against the crash log, not a redacted list.
// Decryption failures on individual rows are skipped rather than aborting
// the whole read, mirroring ListEnvVars' own best-effort behaviour.
func (h *Handler) appEnvVarsByNamespace(ctx context.Context, namespace, appName string) (map[string]string, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT ev.key, ev.value_encrypted
		 FROM env_vars ev
		 JOIN environments e ON e.id = ev.environment_id
		 WHERE e.namespace = $1 AND ev.app_name = $2`,
		namespace, appName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var key string
		var encrypted []byte
		if scanErr := rows.Scan(&key, &encrypted); scanErr != nil {
			return nil, scanErr
		}
		plain, decErr := crypto.DecryptToken(h.cfg.GitopsEncryptionKey, encrypted)
		if decErr != nil {
			continue
		}
		out[key] = string(plain)
	}
	return out, rows.Err()
}
