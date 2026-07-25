package main

import "context"

// Step is one idempotent, dry-runnable unit of a move.
type Step interface {
	ID() string
	Describe() string
	Run(ctx context.Context, r CommandRunner, dryRun bool) error
}

// BuildPlan assembles the ordered steps for a move. Volume steps are included
// only when cfg.Volumes is non-empty.
func BuildPlan(cfg MoveConfig) []Step {
	var steps []Step
	steps = append(steps, &safetyDumpStep{cfg: cfg})
	if len(cfg.Volumes) > 0 {
		steps = append(steps, &longhornBackupStep{cfg: cfg})
		steps = append(steps, &scaleDownStep{cfg: cfg})
		for _, v := range cfg.Volumes {
			steps = append(steps, &volumeCopyStep{cfg: cfg, vol: v})
		}
	}
	steps = append(steps, &copySecretsStep{cfg: cfg})
	steps = append(steps, &captureDBCredsStep{cfg: cfg})
	steps = append(steps, &folderMoveStep{cfg: cfg})
	steps = append(steps, &repatchDBCredsStep{cfg: cfg})
	steps = append(steps, &verifyStep{cfg: cfg})
	steps = append(steps, &teardownStep{cfg: cfg})
	return steps
}
