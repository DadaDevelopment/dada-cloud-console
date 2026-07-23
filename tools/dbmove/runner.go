package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CommandRunner runs an external command and returns combined stdout.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// execRunner runs commands for real.
type execRunner struct{}

// Run executes name+args and returns trimmed combined output.
func (execRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

// fakeRunner records calls and returns canned output keyed by the space-joined
// command prefix, for tests.
type fakeRunner struct {
	calls [][]string
	out   map[string]string
	err   map[string]error
}

// newFakeRunner builds an empty fakeRunner.
func newFakeRunner() *fakeRunner {
	return &fakeRunner{out: map[string]string{}, err: map[string]error{}}
}

// Run records the call and returns the longest-prefix canned response.
func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	joined := strings.Join(call, " ")
	for k, v := range f.out {
		if strings.HasPrefix(joined, k) {
			return v, f.err[k]
		}
	}
	for k, e := range f.err {
		if strings.HasPrefix(joined, k) {
			return "", e
		}
	}
	return "", nil
}

// runWithStdin runs a command feeding stdin, returning combined output.
func runWithStdin(ctx context.Context, r CommandRunner, stdin string, name string, args ...string) (string, error) {
	if er, ok := r.(execRunner); ok {
		return er.runStdin(ctx, stdin, name, args...)
	}
	full := append([]string{name}, args...)
	return r.Run(ctx, full[0], full[1:]...)
}

// runStdin executes name+args feeding stdin, returning trimmed combined output.
func (execRunner) runStdin(ctx context.Context, stdin string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

// waitActionSet polls the newest dbmove-labelled ActionSet until complete or timeout.
func waitActionSet(ctx context.Context, r CommandRunner, kctx, app string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := r.Run(ctx, "kubectl", "--context", kctx, "-n", "databases", "get", "actionset",
			"-l", "dada.io/dbmove="+app, "--sort-by=.metadata.creationTimestamp",
			"-o", "jsonpath={.items[-1:].status.state}")
		if err == nil && strings.Contains(out, "complete") {
			return nil
		}
		if err == nil && strings.Contains(out, "failed") {
			return fmt.Errorf("backup actionset failed for %s", app)
		}
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("backup actionset for %s did not complete in %s", app, timeout)
}
