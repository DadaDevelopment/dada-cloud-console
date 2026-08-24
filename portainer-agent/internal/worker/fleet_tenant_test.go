package worker

import (
	"testing"
	"time"

	"github.com/dada-tuda/console/portainer-agent/internal/db"
	"github.com/google/uuid"
)

func srv(name, ip, source string) db.AppServerRow {
	row := db.AppServerRow{ID: uuid.New(), ProjectID: uuid.New(), Name: name, Source: source}
	if ip != "" {
		row.VMIP = &ip
	}
	return row
}

func TestTenantTargetsSkipsUnreachableHosts(t *testing.T) {
	got := tenantTargets([]db.AppServerRow{
		srv("terraform-vm", "10.0.0.1", "terraform"),
		srv("manual-vm", "10.0.0.2", "manual"),
		srv("no-ip", "", "terraform"),
	})
	if len(got) != 1 || got[0].Name != "terraform-vm" {
		names := []string{}
		for _, s := range got {
			names = append(names, s.Name)
		}
		t.Fatalf("tenantTargets = %v, want only [terraform-vm]", names)
	}
}

func TestTenantAttemptDueRunsOncePerServerUntilItSucceeds(t *testing.T) {
	now := time.Unix(1700000000, 0)
	w := &VMWatcher{clock: func() time.Time { return now }}
	id := uuid.New()

	if !w.tenantAttemptDue(id) {
		t.Fatal("first attempt must be due")
	}
	if w.tenantAttemptDue(id) {
		t.Fatal("failed attempt must not retry on the next tick")
	}
	now = now.Add(tenantRetryInterval + time.Second)
	if !w.tenantAttemptDue(id) {
		t.Fatal("failed attempt must retry after the backoff window")
	}

	w.markTenantApplied(id)
	now = now.Add(30 * 24 * time.Hour)
	if w.tenantAttemptDue(id) {
		t.Fatal("applied server must never be contacted again")
	}
}
