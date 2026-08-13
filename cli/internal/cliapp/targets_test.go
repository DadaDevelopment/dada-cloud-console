package cliapp

import (
	"testing"
)

func TestRememberAndLookupTarget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	if _, ok, err := LookupTarget(dir); err != nil || ok {
		t.Fatalf("fresh folder should have no target: ok=%v err=%v", ok, err)
	}

	want := Target{
		ProjectID:   "p-1",
		ProjectName: "demo",
		EnvID:       "e-1",
		EnvName:     "prod",
		AppName:     "demo",
	}
	if err := RememberTarget(dir, want); err != nil {
		t.Fatal(err)
	}

	got, ok, err := LookupTarget(dir)
	if err != nil || !ok {
		t.Fatalf("remembered target not found: ok=%v err=%v", ok, err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	other := t.TempDir()
	if _, ok, _ := LookupTarget(other); ok {
		t.Fatal("target leaked into an unrelated folder")
	}
}

func TestRememberTargetOverwrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	if err := RememberTarget(dir, Target{ProjectID: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := RememberTarget(dir, Target{ProjectID: "new"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LookupTarget(dir)
	if err != nil || !ok || got.ProjectID != "new" {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
}
