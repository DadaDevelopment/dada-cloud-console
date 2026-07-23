package main

import (
	"context"
	"os/exec"
	"strings"
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
