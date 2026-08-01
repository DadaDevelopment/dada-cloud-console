package db

import (
	"os"
	"regexp"
	"testing"

	"github.com/google/uuid"
)

func TestUnattendedOnlyMatchesTheSystemActor(t *testing.T) {
	if !(Operation{ActorID: SystemActorID}).Unattended() {
		t.Fatal("an operation filed by the platform must read as unattended")
	}
	if (Operation{ActorID: uuid.New()}).Unattended() {
		t.Fatal("an operation filed by a person must not read as unattended")
	}
}

const backendSystemActorPath = "../../../backend/internal/api/deploy_hooks.go"

var backendSystemActorRe = regexp.MustCompile(`systemDeployActorID\s*=\s*uuid\.MustParse\("([^"]+)"\)`)

// TestSystemActorMatchesBackend pins this module's system-actor UUID to the one
// the backend files platform operations under. The two are separate Go modules
// and cannot import each other, so nothing but this test stops them drifting --
// and if they drift, every unattended deploy silently reads as human-initiated
// and the render-clobber guard stops running.
func TestSystemActorMatchesBackend(t *testing.T) {
	src, err := os.ReadFile(backendSystemActorPath)
	if err != nil {
		t.Skipf("backend module not checked out alongside: %v", err)
	}
	m := backendSystemActorRe.FindSubmatch(src)
	if m == nil {
		t.Fatalf("systemDeployActorID no longer declared in %s the way this test reads it", backendSystemActorPath)
	}
	if got := string(m[1]); got != SystemActorID.String() {
		t.Fatalf("backend system actor %s != gitops-agent %s", got, SystemActorID)
	}
}
