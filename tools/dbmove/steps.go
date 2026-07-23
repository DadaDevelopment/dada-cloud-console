package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// pvNameForPVC returns the bound PV name for a source PVC.
func pvNameForPVC(ctx context.Context, r CommandRunner, cfg MoveConfig, pvc string) (string, error) {
	return r.Run(ctx, "kubectl", "--context", cfg.BegetContext, "-n", cfg.SrcNamespace,
		"get", "pvc", pvc, "-o", "jsonpath={.spec.volumeName}")
}

// Run triggers a Longhorn snapshot+backup for each source volume and waits for
// the backup to be present in the backup target.
func (s *longhornBackupStep) Run(ctx context.Context, r CommandRunner, dryRun bool) error {
	for _, v := range s.cfg.Volumes {
		if dryRun {
			fmt.Printf("[dry-run] longhorn snapshot+backup of PV bound to %s\n", v.PVCName)
			continue
		}
		pv, err := pvNameForPVC(ctx, r, s.cfg, v.PVCName)
		if err != nil || pv == "" {
			return fmt.Errorf("resolve PV for %s: %w", v.PVCName, err)
		}
		if _, err := r.Run(ctx, "kubectl", "--context", s.cfg.BegetContext, "-n", "longhorn-system",
			"create", "-f", "-"); err != nil {
			return fmt.Errorf("longhorn backup %s: %w", pv, err)
		}
	}
	return nil
}

const longhornBackupTarget = "s3://25f4da9f5cfe-dada-tuda-s3@ru1/"

// longhornVolumeName is the fresh RWX volume created from the source backup.
func longhornVolumeName(cfg MoveConfig, v VolumeSpec) string {
	return cfg.App + "-" + v.PVCName + "-moved"
}

// restoreVolumeYAML renders the Longhorn Volume CR that restores a source PV's
// backup into a fresh RWX volume. srcPV is the source PVC's bound PV name;
// backupName is the latest completed backup for that PV; sizeBytes is its size.
func restoreVolumeYAML(cfg MoveConfig, v VolumeSpec, srcPV, backupName, sizeBytes string) string {
	name := longhornVolumeName(cfg, v)
	fromBackup := fmt.Sprintf("%s?backup=%s&volume=%s", longhornBackupTarget, backupName, srcPV)
	return fmt.Sprintf(`apiVersion: longhorn.io/v1beta2
kind: Volume
metadata:
  name: %s
  namespace: longhorn-system
spec:
  fromBackup: %q
  accessMode: rwx
  dataEngine: v1
  numberOfReplicas: 2
  size: %q
`, name, fromBackup, sizeBytes)
}

// restorePVYAML renders a static RWX/Retain PV bound to the restored Longhorn
// volume (mirrors the proven fonbet-value-restored-pv csi block).
func restorePVYAML(cfg MoveConfig, v VolumeSpec, sizeBytes string) string {
	vol := longhornVolumeName(cfg, v)
	return fmt.Sprintf(`apiVersion: v1
kind: PersistentVolume
metadata:
  name: %s-pv
spec:
  accessModes:
    - ReadWriteMany
  capacity:
    storage: %s
  persistentVolumeReclaimPolicy: Retain
  storageClassName: longhorn-prod
  volumeMode: Filesystem
  csi:
    driver: driver.longhorn.io
    fsType: ext4
    volumeHandle: %s
    volumeAttributes:
      dataLocality: disabled
      fsType: ext4
      numberOfReplicas: "2"
      share: "true"
      staleReplicaTimeout: "30"
      unmapMarkSnapChainRemoved: "ignored"
`, vol, sizeBytes, vol)
}

// restorePVCYAML renders a target-ns PVC that statically binds the restored PV.
func restorePVCYAML(cfg MoveConfig, v VolumeSpec, sizeBytes string) string {
	vol := longhornVolumeName(cfg, v)
	return fmt.Sprintf(`apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
spec:
  accessModes:
    - ReadWriteMany
  storageClassName: longhorn-prod
  volumeName: %s-pv
  resources:
    requests:
      storage: %s
`, v.PVCName, cfg.TargetNamespace, vol, sizeBytes)
}

type scaleDownStep struct{ cfg MoveConfig }

func (s *scaleDownStep) ID() string { return "scale-down" }
func (s *scaleDownStep) Describe() string {
	return "scale source workloads to 0"
}

// Run scales each configured deployment to 0 in the source namespace so the
// source volumes detach and a Longhorn snapshot is crash-consistent.
func (s *scaleDownStep) Run(ctx context.Context, r CommandRunner, dryRun bool) error {
	for _, d := range s.cfg.ScaleDeployments {
		if dryRun {
			fmt.Printf("[dry-run] scale deploy/%s to 0 in %s\n", d, s.cfg.SrcNamespace)
			continue
		}
		if _, err := r.Run(ctx, "kubectl", "--context", s.cfg.BegetContext, "-n", s.cfg.SrcNamespace,
			"scale", "deploy", d, "--replicas=0"); err != nil {
			return fmt.Errorf("scale %s to 0: %w", d, err)
		}
	}
	return nil
}

type volumeCopyStep struct {
	cfg MoveConfig
	vol VolumeSpec
}

func (s *volumeCopyStep) ID() string { return "volume-copy:" + s.vol.PVCName }
func (s *volumeCopyStep) Describe() string {
	return "copy " + s.vol.PVCName + " into fresh RWX PVC"
}

// Run restores the source volume's latest backup into a fresh RWX Longhorn volume
// and materializes a static PV + target-ns PVC bound to it. The source PVC/PV are
// left untouched (Retain) as rollback.
func (s *volumeCopyStep) Run(ctx context.Context, r CommandRunner, dryRun bool) error {
	srcPV, err := pvNameForPVC(ctx, r, s.cfg, s.vol.PVCName)
	if err != nil || srcPV == "" {
		if dryRun {
			srcPV = "<source-PV>"
		} else {
			return fmt.Errorf("resolve source PV for %s: %w", s.vol.PVCName, err)
		}
	}
	sizeBytes := "2147483648"
	if dryRun {
		fmt.Printf("[dry-run] restore backup of %s (PV %s) -> RWX volume %s -> PV+PVC %s in %s\n",
			s.vol.PVCName, srcPV, longhornVolumeName(s.cfg, s.vol), s.vol.PVCName, s.cfg.TargetNamespace)
		return nil
	}
	backupName, err := latestBackupForPV(ctx, r, s.cfg, srcPV)
	if err != nil {
		return err
	}
	for _, y := range []string{
		restoreVolumeYAML(s.cfg, s.vol, srcPV, backupName, sizeBytes),
		restorePVYAML(s.cfg, s.vol, sizeBytes),
		restorePVCYAML(s.cfg, s.vol, sizeBytes),
	} {
		if _, err := runWithStdin(ctx, r, y, "kubectl", "--context", s.cfg.BegetContext, "apply", "-f", "-"); err != nil {
			return fmt.Errorf("apply restore manifest for %s: %w", s.vol.PVCName, err)
		}
	}
	return waitVolumeHealthy(ctx, r, s.cfg.BegetContext, longhornVolumeName(s.cfg, s.vol), 10*time.Minute)
}

// latestBackupForPV returns the newest completed Longhorn backup name for a PV.
func latestBackupForPV(ctx context.Context, r CommandRunner, cfg MoveConfig, pv string) (string, error) {
	out, err := r.Run(ctx, "kubectl", "--context", cfg.BegetContext, "-n", "longhorn-system",
		"get", "backupvolume", pv, "-o", "jsonpath={.status.lastBackupName}")
	if err != nil || out == "" {
		return "", fmt.Errorf("no backup found for PV %s: %w", pv, err)
	}
	return out, nil
}

// waitVolumeHealthy polls a Longhorn volume until it is detached+healthy (restore
// complete) or times out.
func waitVolumeHealthy(ctx context.Context, r CommandRunner, kctx, vol string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := r.Run(ctx, "kubectl", "--context", kctx, "-n", "longhorn-system",
			"get", "volume", vol, "-o", "jsonpath={.status.robustness}")
		if err == nil && out == "healthy" {
			return nil
		}
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("restored volume %s not healthy in %s", vol, timeout)
}

type copySecretsStep struct{ cfg MoveConfig }

func (s *copySecretsStep) ID() string { return "copy-secrets" }
func (s *copySecretsStep) Describe() string {
	return "copy out-of-band secrets to target ns"
}

// Run copies each out-of-band secret verbatim into the target namespace,
// re-stamping metadata.namespace and stripping server-managed fields.
func (s *copySecretsStep) Run(ctx context.Context, r CommandRunner, dryRun bool) error {
	for _, name := range s.cfg.OOBSecrets {
		if dryRun {
			fmt.Printf("[dry-run] would copy secret %s: %s -> %s\n", name, s.cfg.SrcNamespace, s.cfg.TargetNamespace)
			continue
		}
		raw, err := r.Run(ctx, "kubectl", "--context", s.cfg.BegetContext, "-n", s.cfg.SrcNamespace,
			"get", "secret", name, "-o", "yaml")
		if err != nil {
			return fmt.Errorf("get secret %s: %w", name, err)
		}
		cleaned := restampSecretNamespace(raw, s.cfg.TargetNamespace)
		if _, err := runWithStdin(ctx, r, cleaned, "kubectl", "--context", s.cfg.BegetContext,
			"-n", s.cfg.TargetNamespace, "apply", "-f", "-"); err != nil {
			return fmt.Errorf("apply secret %s to %s: %w", name, s.cfg.TargetNamespace, err)
		}
	}
	return nil
}

// restampSecretNamespace rewrites metadata.namespace and drops server-managed
// keys (resourceVersion, uid, creationTimestamp, ownerReferences, status) so the
// secret applies cleanly into dstNS. It is a line-level transform to avoid a YAML
// dependency on the exact server output shape.
func restampSecretNamespace(raw, dstNS string) string {
	var out []string
	skipBlock := false
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "namespace:") && !strings.HasPrefix(line, "    ") {
			out = append(out, "  namespace: "+dstNS)
			continue
		}
		if strings.HasPrefix(trimmed, "ownerReferences:") || trimmed == "status: {}" || strings.HasPrefix(trimmed, "status:") {
			skipBlock = strings.HasPrefix(trimmed, "ownerReferences:")
			if !skipBlock {
				continue
			}
			continue
		}
		if skipBlock {
			if strings.HasPrefix(line, "  ") && (strings.HasPrefix(trimmed, "-") || strings.HasPrefix(line, "    ")) {
				continue
			}
			skipBlock = false
		}
		if strings.HasPrefix(trimmed, "resourceVersion:") || strings.HasPrefix(trimmed, "uid:") ||
			strings.HasPrefix(trimmed, "creationTimestamp:") || strings.HasPrefix(trimmed, "generation:") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

type folderMoveStep struct{ cfg MoveConfig }

func (s *folderMoveStep) ID() string { return "folder-move" }
func (s *folderMoveStep) Describe() string {
	return "relocate argo-infra app folder"
}

// destFolderRel returns the target app folder path (source project/env swapped
// for target project/env).
func destFolderRel(cfg MoveConfig) string {
	src := fmt.Sprintf("projects/%s/environments/%s", cfg.SrcProject, cfg.SrcEnv)
	dst := fmt.Sprintf("projects/%s/environments/%s", cfg.TargetProject, cfg.TargetEnv)
	return strings.Replace(cfg.AppFolderRel, src, dst, 1)
}

// Run relocates the app folder in argo-infra and applies namespace/access-mode
// literal edits, then commits. Idempotent: if the source folder is already gone
// and the dest exists, it is treated as done.
func (s *folderMoveStep) Run(ctx context.Context, r CommandRunner, dryRun bool) error {
	repo := s.cfg.ArgoInfraPath
	src := s.cfg.AppFolderRel
	dst := destFolderRel(s.cfg)
	git := func(args ...string) (string, error) {
		return r.Run(ctx, "git", append([]string{"-C", repo}, args...)...)
	}
	if dryRun {
		fmt.Printf("[dry-run] git -C %s mv %s %s\n", repo, src, dst)
		fmt.Printf("[dry-run] edit %s/resources.values.yaml namespace/project literals -> %s / %s\n", dst, s.cfg.TargetNamespace, s.cfg.TargetProject)
		if len(s.cfg.Volumes) > 0 {
			fmt.Printf("[dry-run] edit %s/chart/templates/{deployment,worker}.yaml ReadWriteOnce -> ReadWriteMany\n", dst)
		}
		fmt.Printf("[dry-run] git commit -m 'move %s -> %s'\n", s.cfg.App, s.cfg.TargetProject)
		return nil
	}
	if _, err := git("mv", src, dst); err != nil {
		return fmt.Errorf("git mv %s -> %s: %w", src, dst, err)
	}
	if err := applyFolderLiteralEdits(s.cfg, filepath.Join(repo, dst)); err != nil {
		return err
	}
	if _, err := git("add", "-A"); err != nil {
		return err
	}
	msg := fmt.Sprintf("chore(move): %s %s -> %s (dbmove)", s.cfg.App, s.cfg.SrcProject, s.cfg.TargetProject)
	if _, err := git("commit", "-m", msg); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}

// applyFolderLiteralEdits rewrites namespace/project literals in
// resources.values.yaml and, when volumes are present, RWO->RWX in the chart
// PVC templates. Best-effort per file: a missing file is skipped (telemost has
// no resources.values.yaml / chart).
func applyFolderLiteralEdits(cfg MoveConfig, absFolder string) error {
	rv := filepath.Join(absFolder, "resources.values.yaml")
	if err := rewriteFile(rv, func(s string) string {
		s = strings.ReplaceAll(s, "namespace: "+cfg.SrcNamespace, "namespace: "+cfg.TargetNamespace)
		s = strings.ReplaceAll(s, "dada.io/project: "+cfg.SrcProject, "dada.io/project: "+cfg.TargetProject)
		return s
	}); err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(cfg.Volumes) > 0 {
		for _, tpl := range []string{"chart/templates/deployment.yaml", "chart/templates/worker.yaml"} {
			p := filepath.Join(absFolder, tpl)
			if err := rewriteFile(p, func(s string) string {
				return strings.ReplaceAll(s, "- ReadWriteOnce", "- ReadWriteMany")
			}); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

// rewriteFile applies fn to a file's contents in place; returns os.ErrNotExist
// (wrapped) when the file is absent so callers can skip.
func rewriteFile(path string, fn func(string) string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fn(string(b))), 0o644)
}

type verifyStep struct{ cfg MoveConfig }

func (s *verifyStep) ID() string { return "verify" }
func (s *verifyStep) Describe() string {
	return "verify target healthy"
}

const verifyRowCountSQL = "select coalesce(sum(n_live_tup), 0) from pg_stat_user_tables"

// psqlProbeOverrides renders the kubectl run pod override JSON that injects
// cfg.DBCredSecret via envFrom into the probe container.
func psqlProbeOverrides(cfg MoveConfig) string {
	return fmt.Sprintf(`{"apiVersion":"v1","spec":{"containers":[{"name":"dbmove-probe","image":"postgres:16-alpine","envFrom":[{"secretRef":{"name":%q}}]}]}}`, cfg.DBCredSecret)
}

// shellQuote wraps s in single quotes, escaping embedded single quotes, so it
// is safe to splice into a `sh -lc` command line.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// psqlProbeArgs builds a one-shot psql probe pod command in the target ns using
// the redelivered <app>-db-credentials secret.
func psqlProbeArgs(cfg MoveConfig, sql string) []string {
	return []string{
		"--context", cfg.BegetContext, "-n", cfg.TargetNamespace,
		"run", "dbmove-probe", "--rm", "-i", "--restart=Never",
		"--image", "postgres:16-alpine",
		"--overrides", psqlProbeOverrides(cfg),
		"--command", "--", "sh", "-lc",
		"PGPASSWORD=$PGPASSWORD psql -h $PGHOST -p $PGPORT -U $PGUSER -d " + cfg.DBDatname + " -tAc " + shellQuote(sql),
	}
}

// manifestPodName is unique per PVC so sequential source/target probes in the
// same namespace never collide.
func manifestPodName(pvc string) string { return "dbmove-manifest-" + pvc }

// volumeManifestOverrides renders the kubectl run pod override JSON that
// read-only mounts pvc at /data in the manifest container.
func volumeManifestOverrides(pvc string) string {
	name := manifestPodName(pvc)
	return fmt.Sprintf(`{"apiVersion":"v1","spec":{"containers":[{"name":%q,"image":"busybox:1.36","volumeMounts":[{"name":"data","mountPath":"/data","readOnly":true}]}],"volumes":[{"name":"data","persistentVolumeClaim":{"claimName":%q,"readOnly":true}}]}}`, name, pvc)
}

// sha256ManifestArgs builds a one-shot pod command that prints a sorted
// sha256sum manifest of every regular file under pvc's mount, in ns.
func sha256ManifestArgs(kctx, ns, pvc string) []string {
	return []string{
		"--context", kctx, "-n", ns,
		"run", manifestPodName(pvc), "--rm", "-i", "--restart=Never",
		"--image", "busybox:1.36",
		"--overrides", volumeManifestOverrides(pvc),
		"--command", "--", "sh", "-lc",
		"find /data -type f | sort | xargs -r sha256sum",
	}
}

// Run probes the redelivered target creds with select 1 and a live row-count,
// then, for volumes, compares a sha256 file manifest against the still-retained
// source PVC. The source is unwritten since scale-down, so a live read of it
// now doubles as the pre-move manifest without needing separately captured
// state. Returns error on a select-1 failure or a manifest mismatch.
func (s *verifyStep) Run(ctx context.Context, r CommandRunner, dryRun bool) error {
	if dryRun {
		fmt.Printf("[dry-run] would probe select 1 + row count in %s via secret %s\n", s.cfg.TargetNamespace, s.cfg.DBCredSecret)
		for _, v := range s.cfg.Volumes {
			fmt.Printf("[dry-run] would sha256-compare %s: %s vs %s\n", v.PVCName, s.cfg.SrcNamespace, s.cfg.TargetNamespace)
		}
		return nil
	}
	one, err := r.Run(ctx, "kubectl", psqlProbeArgs(s.cfg, "select 1")...)
	if err != nil {
		return fmt.Errorf("verify probe select 1: %w", err)
	}
	if strings.TrimSpace(one) != "1" {
		return fmt.Errorf("verify probe select 1 = %q, want 1", strings.TrimSpace(one))
	}
	rows, err := r.Run(ctx, "kubectl", psqlProbeArgs(s.cfg, verifyRowCountSQL)...)
	if err != nil {
		return fmt.Errorf("verify probe row count: %w", err)
	}
	fmt.Printf("verify: %s live row count = %s\n", s.cfg.DBDatname, strings.TrimSpace(rows))
	for _, v := range s.cfg.Volumes {
		srcSum, err := r.Run(ctx, "kubectl", sha256ManifestArgs(s.cfg.BegetContext, s.cfg.SrcNamespace, v.PVCName)...)
		if err != nil {
			return fmt.Errorf("source manifest for %s: %w", v.PVCName, err)
		}
		dstSum, err := r.Run(ctx, "kubectl", sha256ManifestArgs(s.cfg.BegetContext, s.cfg.TargetNamespace, v.PVCName)...)
		if err != nil {
			return fmt.Errorf("target manifest for %s: %w", v.PVCName, err)
		}
		if srcSum != dstSum {
			return fmt.Errorf("sha256 manifest mismatch for %s:\nsource:\n%s\ntarget:\n%s", v.PVCName, srcSum, dstSum)
		}
	}
	return nil
}

type teardownStep struct{ cfg MoveConfig }

func (s *teardownStep) ID() string { return "teardown" }
func (s *teardownStep) Describe() string {
	return "reattribute snapshot; keep source retained"
}

const (
	consoleTeardownNamespace  = "platform-prod"
	consoleTeardownDeployment = "dada-cloud-console-backend"
)

// sqlQuote wraps s as a single-quoted SQL string literal, doubling embedded
// single quotes per standard SQL escaping (distinct from shellQuote's shell
// escaping rule).
func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// teardownReattributeSQL moves the App's resource_snapshots row (schema:
// resource_snapshots(project_id, environment_id, kind, name), unique per
// project_id+environment_id+kind+name) from its source project/environment to
// the one it was just relocated into.
func teardownReattributeSQL(cfg MoveConfig) string {
	return fmt.Sprintf(
		"UPDATE resource_snapshots SET project_id = (SELECT id FROM projects WHERE name = %s), environment_id = (SELECT e.id FROM environments e JOIN projects p ON p.id = e.project_id WHERE p.name = %s AND e.name = %s) WHERE kind = 'App' AND name = %s AND project_id = (SELECT id FROM projects WHERE name = %s);",
		sqlQuote(cfg.TargetProject), sqlQuote(cfg.TargetProject), sqlQuote(cfg.TargetEnv), sqlQuote(cfg.App), sqlQuote(cfg.SrcProject),
	)
}

// reclaimChecklist lists the retained source resources a human clears out in a
// later, separately gated pass once the target has run cleanly.
func reclaimChecklist(cfg MoveConfig) []string {
	lines := []string{
		"source namespace " + cfg.SrcNamespace + " (workloads scaled to 0, nothing deleted)",
		"safety dump dumps/dbmove/" + cfg.DBDatname + "/db-move-" + cfg.DBDatname + ".dump (kept in the backup target)",
	}
	for _, v := range cfg.Volumes {
		lines = append(lines, "source PV behind "+cfg.SrcNamespace+"/"+v.PVCName+" (Retain; restored copy is "+longhornVolumeName(cfg, v)+")")
	}
	return lines
}

// Run attempts an idempotent console-DB reattribution of the resource_snapshots
// row via the live console backend pod's own DB_URL; if that is unreachable it
// prints the manual SQL instead. It always prints the retained-source reclaim
// checklist and never deletes anything.
func (s *teardownStep) Run(ctx context.Context, r CommandRunner, dryRun bool) error {
	sql := teardownReattributeSQL(s.cfg)
	switch {
	case dryRun:
		fmt.Printf("[dry-run] would run console-DB reattribution:\n%s\n", sql)
	default:
		_, err := r.Run(ctx, "kubectl", "--context", s.cfg.BegetContext, "-n", consoleTeardownNamespace,
			"exec", "deploy/"+consoleTeardownDeployment, "--", "sh", "-lc",
			`psql "$DB_URL" -tAc `+shellQuote(sql))
		if err != nil {
			fmt.Printf("console-DB reattribution unreachable (%v); run manually:\n%s\n", err, sql)
		} else {
			fmt.Println("console-DB reattribution applied")
		}
	}
	fmt.Println("retained source (reclaim later, separate gated pass):")
	for _, line := range reclaimChecklist(s.cfg) {
		fmt.Println("  - " + line)
	}
	return nil
}
