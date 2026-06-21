package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BuildArtifact is one Android output (APK/AAB) the Jenkins job pushed to the
// Nexus raw repo. The control plane records it after confirming it exists
// (HEAD against Nexus). nexus_url is the absolute raw-repo URL the build emitted
// in its console marker; the backend download endpoint proxies it with
// server-side Nexus creds.
type BuildArtifact struct {
	BuildID     uuid.UUID
	Type        string // apk | aab
	NexusURL    string
	Size        int64
	VersionCode int
	SHA256      string
}

// InsertArtifact records one confirmed Android artifact. Idempotent on
// (build_id, type) so a re-driven build does not duplicate rows.
func InsertArtifact(ctx context.Context, pool *pgxpool.Pool, a BuildArtifact) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO build_artifacts (build_id, type, nexus_url, size, version_code, sha256)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (build_id, type) DO UPDATE
		SET nexus_url = EXCLUDED.nexus_url,
		    size = EXCLUDED.size,
		    version_code = EXCLUDED.version_code,
		    sha256 = EXCLUDED.sha256
	`, a.BuildID, a.Type, a.NexusURL, a.Size, a.VersionCode, a.SHA256)
	if err != nil {
		return fmt.Errorf("insert artifact %s/%s: %w", a.BuildID, a.Type, err)
	}
	return nil
}
