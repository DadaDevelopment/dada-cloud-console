package main

import (
	"context"
	"fmt"
	"time"
)

type safetyDumpStep struct{ cfg MoveConfig }

func (s *safetyDumpStep) ID() string { return "safety-dump" }
func (s *safetyDumpStep) Describe() string {
	return "safety pg_dump of " + s.cfg.DBDatname
}

// backupActionSetYAML renders a Kanister backup ActionSet for the shared
// Postgres StatefulSet, keyed to cfg.DBDatname. dumpPath mirrors the backend
// convention dumps/<scope>/<db>/<name>.dump under the profile prefix.
func backupActionSetYAML(cfg MoveConfig, name string) string {
	return fmt.Sprintf(`apiVersion: cr.kanister.io/v1alpha1
kind: ActionSet
metadata:
  generateName: db-move-backup-
  namespace: databases
  labels:
    dada.io/dbmove: %q
spec:
  actions:
    - name: backup
      blueprint: postgres-logical-db-blueprint
      object:
        kind: StatefulSet
        name: postgresql
        namespace: databases
      profile:
        name: dada-db-backups
        namespace: databases
      options:
        database: %s
        dumpPath: dumps/dbmove/%s/%s.dump
`, cfg.App, cfg.DBDatname, cfg.DBDatname, name)
}

// Run creates the backup ActionSet and waits for completion (skipped on dry-run).
func (s *safetyDumpStep) Run(ctx context.Context, r CommandRunner, dryRun bool) error {
	name := "db-move-" + s.cfg.DBDatname
	y := backupActionSetYAML(s.cfg, name)
	if dryRun {
		fmt.Printf("[dry-run] would create backup ActionSet:\n%s\n", y)
		return nil
	}
	if _, err := runWithStdin(ctx, r, y, "kubectl", "--context", s.cfg.BegetContext, "create", "-f", "-"); err != nil {
		return fmt.Errorf("create backup actionset: %w", err)
	}
	return waitActionSet(ctx, r, s.cfg.BegetContext, s.cfg.App, 15*time.Minute)
}

type longhornBackupStep struct{ cfg MoveConfig }

func (s *longhornBackupStep) ID() string { return "longhorn-backup" }
func (s *longhornBackupStep) Describe() string {
	return "longhorn backup of source volumes"
}
func (s *longhornBackupStep) Run(context.Context, CommandRunner, bool) error { return nil }

type scaleDownStep struct{ cfg MoveConfig }

func (s *scaleDownStep) ID() string { return "scale-down" }
func (s *scaleDownStep) Describe() string {
	return "scale source workloads to 0"
}
func (s *scaleDownStep) Run(context.Context, CommandRunner, bool) error { return nil }

type volumeCopyStep struct {
	cfg MoveConfig
	vol VolumeSpec
}

func (s *volumeCopyStep) ID() string { return "volume-copy:" + s.vol.PVCName }
func (s *volumeCopyStep) Describe() string {
	return "copy " + s.vol.PVCName + " into fresh RWX PVC"
}
func (s *volumeCopyStep) Run(context.Context, CommandRunner, bool) error { return nil }

type copySecretsStep struct{ cfg MoveConfig }

func (s *copySecretsStep) ID() string { return "copy-secrets" }
func (s *copySecretsStep) Describe() string {
	return "copy out-of-band secrets to target ns"
}
func (s *copySecretsStep) Run(context.Context, CommandRunner, bool) error { return nil }

type folderMoveStep struct{ cfg MoveConfig }

func (s *folderMoveStep) ID() string { return "folder-move" }
func (s *folderMoveStep) Describe() string {
	return "relocate argo-infra app folder"
}
func (s *folderMoveStep) Run(context.Context, CommandRunner, bool) error { return nil }

type verifyStep struct{ cfg MoveConfig }

func (s *verifyStep) ID() string { return "verify" }
func (s *verifyStep) Describe() string {
	return "verify target healthy"
}
func (s *verifyStep) Run(context.Context, CommandRunner, bool) error { return nil }

type teardownStep struct{ cfg MoveConfig }

func (s *teardownStep) ID() string { return "teardown" }
func (s *teardownStep) Describe() string {
	return "reattribute snapshot; keep source retained"
}
func (s *teardownStep) Run(context.Context, CommandRunner, bool) error { return nil }
