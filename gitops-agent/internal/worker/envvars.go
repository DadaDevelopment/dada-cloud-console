package worker

import (
	"context"
	"fmt"

	"github.com/dada-tuda/console/gitops-agent/internal/crypto"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// resolvedEnv holds an app's runtime environment resolved (and decrypted) at
// render time from the env_vars table. Plain holds non-sensitive vars (safe to
// commit to git); Secret holds sensitive vars (is_secret=true), kept separate so
// they go out-of-band (a k8s Secret manifest / .env), never into values.yaml.
type resolvedEnv struct {
	Plain  map[string]string
	Secret map[string]string
	// Refs are variables whose value lives in a k8s Secret the console does not
	// own (env_vars rows carrying secret_ref_name/secret_ref_key and no value).
	// They exist so an app that was never created through the console can be
	// adopted without moving anyone's credentials: the reference travels, the
	// value never does.
	Refs []renderer.SecretRefEnvVar
}

func (e resolvedEnv) hasSecret() bool { return len(e.Secret) > 0 }

// applyTo puts a resolved environment onto an app spec: plain vars inline,
// sensitive vars behind the per-app console Secret, and adopted vars as the
// foreign references they already were. Every render path shares it so a new
// kind of variable cannot reach three of four callers and silently vanish from
// the fourth.
func (e resolvedEnv) applyTo(spec *renderer.AppSpec, appName string) {
	spec.Env = e.Plain
	spec.SecretRefEnv = e.Refs
	if !e.hasSecret() {
		return
	}
	spec.SecretEnvName = renderer.AppEnvSecretName(appName)
	for k := range e.Secret {
		spec.SecretEnvKeys = append(spec.SecretEnvKeys, k)
	}
}

// resolveRuntimeEnv loads env_vars for (environment_id, app_name) with scope IN
// ('runtime','both'), decrypts each value with the gitops encryption key, and
// splits sensitive (is_secret) from non-sensitive. Resolution happens HERE at
// render time — env vars are NEVER carried in operations.payload (which is
// plaintext). Returns empty (non-nil) maps when there are no vars.
//
// Decryption: env_vars.value_encrypted is ALWAYS AES-GCM (even for non-sensitive
// rows), same format as git_integrations.token_encrypted (crypto.DecryptToken).
func (w *DBWatcher) resolveRuntimeEnv(ctx context.Context, environmentID *uuid.UUID, appName string) (resolvedEnv, error) {
	out := resolvedEnv{Plain: map[string]string{}, Secret: map[string]string{}}
	if environmentID == nil {
		return out, nil
	}
	rows, err := w.pool.Query(ctx, `
		SELECT key, value_encrypted, is_secret, secret_ref_name, secret_ref_key
		FROM env_vars
		WHERE environment_id = $1 AND app_name = $2 AND scope IN ('runtime', 'both')
		ORDER BY key
	`, *environmentID, appName)
	if err != nil {
		return out, fmt.Errorf("query env_vars: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			key      string
			enc      []byte
			isSecret bool
			refName  *string
			refKey   *string
		)
		if err := rows.Scan(&key, &enc, &isSecret, &refName, &refKey); err != nil {
			return out, fmt.Errorf("scan env_var: %w", err)
		}
		if refName != nil && refKey != nil {
			out.Refs = append(out.Refs, renderer.SecretRefEnvVar{
				Name: key, SecretName: *refName, SecretKey: *refKey,
			})
			continue
		}
		val, err := crypto.DecryptToken(w.cfg.EncryptionKey, enc)
		if err != nil {
			// Do not log the value; a decrypt failure usually means a key mismatch.
			return out, fmt.Errorf("decrypt env_var %q: %w", key, err)
		}
		if isSecret {
			out.Secret[key] = val
		} else {
			out.Plain[key] = val
		}
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("iterate env_vars: %w", err)
	}
	if out.hasSecret() {
		log.Debug().Str("app", appName).Int("secret_count", len(out.Secret)).
			Msg("resolved sensitive runtime env — rendering plaintext Secret into git (no kube/SealedSecret channel)")
	}
	return out, nil
}

// renderEnvSecretFile reconciles the per-app sensitive-env Secret inside the
// app's resources.values.yaml manifests list. When env has sensitive vars it
// upserts the Secret CR; when none remain it removes any prior Secret entry so a
// deploy that drops the last secret cleans up. Returns nil when nothing changed
// (no secrets and no prior Secret to remove), so the caller can skip the file.
//
// SECURITY: the Secret carries PLAINTEXT in stringData committed to git (no
// kube/SealedSecret channel in gitops-agent) — see renderer.RenderAppEnvSecret.
func (w *DBWatcher) renderEnvSecretFile(mgr *git.Manager, projectName, envName, namespace, appName, opID string, env resolvedEnv) (*git.FileChange, error) {
	// Ensure the worktree is present so the existing resources.values.yaml (if any)
	// is read for upsert/remove rather than silently treated as absent.
	if err := mgr.EnsureCloned(); err != nil {
		return nil, err
	}
	valuesPath := renderer.AppResourcesValuesGitPath(projectName, envName, appName)
	secretName := renderer.AppEnvSecretName(appName)

	if !env.hasSecret() {
		fc, changed, err := removeManifestsFile(mgr, valuesPath, [][2]string{{"Secret", secretName}})
		if err != nil {
			return nil, err
		}
		if !changed {
			return nil, nil
		}
		return &fc, nil
	}

	secretYAML, err := renderer.RenderAppEnvSecret(renderer.AppEnvSecretSpec{
		Name:        secretName,
		Namespace:   namespace,
		ProjectSlug: projectName,
		EnvSlug:     envName,
		OperationID: opID,
		Data:        env.Secret,
	})
	if err != nil {
		return nil, err
	}
	fc, err := upsertManifestFile(mgr, valuesPath, secretYAML)
	if err != nil {
		return nil, err
	}
	return &fc, nil
}

// merged returns a single map of all runtime env (plain + secret), used for the
// VM/compose .env track where there is no out-of-band secret channel.
func (e resolvedEnv) merged() map[string]string {
	m := make(map[string]string, len(e.Plain)+len(e.Secret))
	for k, v := range e.Plain {
		m[k] = v
	}
	for k, v := range e.Secret {
		m[k] = v
	}
	return m
}

// dryRunEnvPlaceholder stands in for the value of a variable a dry run is
// asking about. A plan reports paths, never values, so the placeholder only has
// to be present and different from whatever is in git.
const dryRunEnvPlaceholder = "(pending value)"

// overlayPendingEnv folds an unwritten env-var change into a resolved
// environment so a dry run describes the write being considered rather than the
// state before it.
//
// A dry run persists nothing -- that is the point of asking first -- so the key
// the caller is about to set does not exist in env_vars while the plan is being
// computed, and a plan computed without it would report every consequence of
// the write except the write itself. Set keys land in Plain with a placeholder:
// the plan names common.extraEnv.<KEY> as added or changed, and no value is ever
// carried, stored or logged.
func overlayPendingEnv(env resolvedEnv, setKeys, unsetKeys []string) {
	for _, k := range unsetKeys {
		delete(env.Plain, k)
		delete(env.Secret, k)
	}
	for _, k := range setKeys {
		if _, secret := env.Secret[k]; secret {
			env.Secret[k] = dryRunEnvPlaceholder
			continue
		}
		env.Plain[k] = dryRunEnvPlaceholder
	}
}
