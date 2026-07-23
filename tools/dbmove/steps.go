package main

import "context"

type safetyDumpStep struct{ cfg MoveConfig }

func (s *safetyDumpStep) ID() string { return "safety-dump" }
func (s *safetyDumpStep) Describe() string {
	return "safety pg_dump of " + s.cfg.DBDatname
}
func (s *safetyDumpStep) Run(context.Context, CommandRunner, bool) error { return nil }

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
