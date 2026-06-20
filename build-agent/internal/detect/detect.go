// Package detect resolves how a repo should be built (Dockerfile vs Nixpacks).
//
// Two surfaces:
//
//   - Resolve: the agent-side decision carried into the Job template (honors an
//     explicit framework_override; otherwise the in-Job entrypoint auto-detects
//     after clone, because untrusted source must not enter the control plane).
//   - Plan: an OPTIONAL, control-plane-side framework preview that shells out to
//     the `nixpacks` binary against an already-checked-out tree. Used by the
//     import wizard ("we detected: Next.js") — never against untrusted code in
//     the agent process; callers pass a trusted local path.
package detect

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Framework is the detected/forced build framework.
type Framework string

const (
	FrameworkDockerfile Framework = "dockerfile"
	FrameworkNixpacks   Framework = "nixpacks"
)

// Result describes how a build should proceed. It is interpolated into the Job
// template; an empty Framework means "let the in-Job entrypoint decide".
type Result struct {
	Framework Framework
	RootDir   string
}

// FrameworkDetection is the rich detection contract surfaced to the import
// wizard (plan §8 framework-card). Provider is the Nixpacks provider name
// (e.g. "node", "python", "go"); BuildCmd/StartCmd are the planned commands.
type FrameworkDetection struct {
	Framework  string   `json:"framework"`           // provider, or "dockerfile"
	Provider   string   `json:"provider,omitempty"`  // nixpacks provider name
	BuildCmd   string   `json:"build_cmd,omitempty"` // planned build command
	StartCmd   string   `json:"start_cmd,omitempty"` // planned start command
	InstallCmd string   `json:"install_cmd,omitempty"`
	Packages   []string `json:"packages,omitempty"` // nix packages the plan pulls in
}

// Resolve picks the framework carried into the Job. An override is honored;
// otherwise the entrypoint auto-detects (Dockerfile present → dockerfile, else
// nixpacks).
func Resolve(frameworkOverride, rootDir string) Result {
	if rootDir == "" {
		rootDir = "."
	}
	r := Result{RootDir: rootDir}
	switch frameworkOverride {
	case string(FrameworkDockerfile):
		r.Framework = FrameworkDockerfile
	case string(FrameworkNixpacks):
		r.Framework = FrameworkNixpacks
	default:
		r.Framework = "" // entrypoint auto-detects
	}
	return r
}

// nixpacksPlan is the subset of `nixpacks plan --format json` output we read.
type nixpacksPlan struct {
	Providers []string `json:"providers"`
	Phases    map[string]struct {
		Cmds      []string `json:"cmds"`
		NixPkgs   []string `json:"nixPkgs"`
		DependsOn []string `json:"dependsOn"`
	} `json:"phases"`
	Start struct {
		Cmd string `json:"cmd"`
	} `json:"start"`
}

// Plan shells out to the `nixpacks` binary to preview the build plan for a
// trusted, already-checked-out source tree at dir. It returns the detected
// framework + planned commands. Returns an error if the binary is unavailable
// or the source matches no provider.
//
// This is the off-the-shelf zero-config detection (plan §"Tech choices").
func Plan(ctx context.Context, dir string) (FrameworkDetection, error) {
	out, err := exec.CommandContext(ctx, "nixpacks", "plan", dir, "--format", "json").Output()
	if err != nil {
		return FrameworkDetection{}, fmt.Errorf("nixpacks plan: %w", err)
	}

	var p nixpacksPlan
	if err := json.Unmarshal(out, &p); err != nil {
		return FrameworkDetection{}, fmt.Errorf("parse nixpacks plan: %w", err)
	}

	d := FrameworkDetection{
		StartCmd: strings.TrimSpace(p.Start.Cmd),
	}
	if len(p.Providers) > 0 {
		d.Provider = p.Providers[0]
		d.Framework = p.Providers[0]
	}
	if ph, ok := p.Phases["install"]; ok && len(ph.Cmds) > 0 {
		d.InstallCmd = strings.Join(ph.Cmds, " && ")
	}
	if ph, ok := p.Phases["build"]; ok && len(ph.Cmds) > 0 {
		d.BuildCmd = strings.Join(ph.Cmds, " && ")
	}
	if ph, ok := p.Phases["setup"]; ok {
		d.Packages = ph.NixPkgs
	}
	if d.Framework == "" {
		return d, fmt.Errorf("nixpacks: no provider detected for %s", dir)
	}
	return d, nil
}
