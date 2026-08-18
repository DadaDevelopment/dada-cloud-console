package api

import (
	"testing"

	"github.com/dada-tuda/console/backend/internal/opencost"
)

func newTestAccumulator() *adminCostsAccumulator {
	return &adminCostsAccumulator{
		clients:   map[string]*adminCostClient{},
		resources: map[string]map[string]map[string]int{},
	}
}

func findResource(acc *adminCostsAccumulator, clientID, projectID, name string) *adminCostResource {
	cl := acc.clients[clientID]
	if cl == nil {
		return nil
	}
	pi := adminCostProjectIndex(cl, projectID)
	if pi < 0 {
		return nil
	}
	for i := range cl.Projects[pi].Resources {
		if cl.Projects[pi].Resources[i].Name == name {
			return &cl.Projects[pi].Resources[i]
		}
	}
	return nil
}

// TestEnsureResourceStablePointerAcrossReallocs guards the position-index
// rewrite: appending later resources reallocates a project's Resources backing
// array, so a re-seen resource must still accumulate onto its original slot.
// The previous pointer-cache implementation wrote to the stale backing array
// and silently dropped the second contribution.
func TestEnsureResourceStablePointerAcrossReallocs(t *testing.T) {
	acc := newTestAccumulator()
	const cl, cn, p, pn = "c1", "Client 1", "p1", "Proj 1"

	acc.add(cl, cn, p, pn, "appA", "app", opencost.Allocation{CPUCost: 10, TotalCost: 10}, 1)
	acc.add(cl, cn, p, pn, "appB", "app", opencost.Allocation{CPUCost: 20, TotalCost: 20}, 1)
	acc.add(cl, cn, p, pn, "appC", "app", opencost.Allocation{CPUCost: 30, TotalCost: 30}, 1)
	acc.add(cl, cn, p, pn, "appA", "app", opencost.Allocation{CPUCost: 5, TotalCost: 5}, 1)

	a := findResource(acc, cl, p, "appA")
	if a == nil {
		t.Fatal("appA missing")
	}
	if a.TotalCost != 15 {
		t.Fatalf("appA TotalCost = %v, want 15 (10 + 5 after a realloc)", a.TotalCost)
	}
	if b := findResource(acc, cl, p, "appB"); b == nil || b.TotalCost != 20 {
		t.Fatalf("appB TotalCost wrong: %+v", b)
	}
	if c := findResource(acc, cl, p, "appC"); c == nil || c.TotalCost != 30 {
		t.Fatalf("appC TotalCost wrong: %+v", c)
	}

	pi := adminCostProjectIndex(acc.clients[cl], p)
	if got := len(acc.clients[cl].Projects[pi].Resources); got != 3 {
		t.Fatalf("resource count = %d, want 3 (appA reused, not duplicated)", got)
	}
}

// TestEnsureResourceJoinsRevenueOntoCostNode proves the cost walk and the
// revenue walk land on the same resource node, so a row's margin is its own
// apps' revenue minus its own cost.
func TestEnsureResourceJoinsRevenueOntoCostNode(t *testing.T) {
	acc := newTestAccumulator()
	const cl, cn, p, pn = "c1", "Client 1", "p1", "Proj 1"

	acc.add(cl, cn, p, pn, "appA", "app", opencost.Allocation{TotalCost: 40}, 1)
	r := acc.ensureResource(cl, cn, p, pn, "appA", "app")
	r.Revenue += 100

	a := findResource(acc, cl, p, "appA")
	if a == nil {
		t.Fatal("appA missing")
	}
	if a.TotalCost != 40 || a.Revenue != 100 {
		t.Fatalf("appA = {cost %v, revenue %v}, want {40, 100}", a.TotalCost, a.Revenue)
	}
	if pi := adminCostProjectIndex(acc.clients[cl], p); len(acc.clients[cl].Projects[pi].Resources) != 1 {
		t.Fatal("revenue walk must reuse the cost node, not append a second appA")
	}
}

func TestSharedDatabaseRevenueUsesCommonMarkup(t *testing.T) {
	if got := sharedDatabaseRevenue(100, 1.5); got != 150 {
		t.Fatalf("database revenue = %v, want 150", got)
	}
}

// TestAdminCostOwnerOfRouting locks the client-vs-platform routing: only a
// project owned by a real user is a billable client; owner-less projects and
// infra namespaces both fall into the single platform own-infrastructure bucket.
func TestAdminCostOwnerOfRouting(t *testing.T) {
	nsMap := map[string]adminCostOwner{
		"acme-prod":     {projectID: "p-acme", projectName: "Acme", ownerID: "u-1", ownerName: "acme@example.com"},
		"acme-pr-7-web": {projectID: "p-acme", projectName: "Acme", ownerID: "u-1", ownerName: "acme@example.com", isPreview: true},
		"platform-prod": {projectID: "p-plat", projectName: "Platform", ownerID: "", ownerName: ""},
	}

	cid, cname, pid, pname := adminCostOwnerOf("acme-prod", nsMap)
	if cid != "u-1" || cname != "acme@example.com" || pid != "p-acme" || pname != "Acme" {
		t.Fatalf("owned project misrouted: got (%q,%q,%q,%q)", cid, cname, pid, pname)
	}

	cid, _, pid, pname = adminCostOwnerOf("acme-pr-7-web", nsMap)
	if cid != platformClientID || pid != "ns:acme-pr-7-web" || pname != "acme-pr-7-web" {
		t.Fatalf("preview namespace of a real customer must route to platform (the free-preview subsidy), not bill the owner: got (%q,%q,%q)", cid, pid, pname)
	}

	cid, _, pid, pname = adminCostOwnerOf("platform-prod", nsMap)
	if cid != platformClientID || pid != "p-plat" || pname != "Platform" {
		t.Fatalf("owner-less project must route to platform bucket, keeping its real project: got (%q,%q,%q)", cid, pid, pname)
	}

	cid, _, pid, pname = adminCostOwnerOf("databases", nsMap)
	if cid != platformClientID || pid != "ns:databases" || pname != "databases" {
		t.Fatalf("infra namespace must route to platform bucket as ns:<name>: got (%q,%q,%q)", cid, pid, pname)
	}
}

// TestPlatformCostOnly proves the platform bucket is blanked to cost-only at
// every level (client, project, resource) while its costs are left untouched --
// the cloud's own infrastructure is not a client and earns no revenue.
func TestPlatformCostOnly(t *testing.T) {
	cl := &adminCostClient{
		ClientID: platformClientID, Cost: 100, Revenue: 40, Margin: -60, MarginPct: -150,
		Projects: []adminCostProject{{
			ProjectID: "ns:databases", Cost: 100, Revenue: 40, Margin: -60, MarginPct: -150,
			Resources: []adminCostResource{{Name: "postgresql-0", Kind: "database", TotalCost: 100, Revenue: 40, Margin: -60, MarginPct: -150}},
		}},
	}
	platformCostOnly(cl)

	if cl.Cost != 100 || cl.Revenue != 0 || cl.Margin != 0 || cl.MarginPct != 0 {
		t.Fatalf("client level not cost-only: %+v", cl)
	}
	p := cl.Projects[0]
	if p.Cost != 100 || p.Revenue != 0 || p.Margin != 0 || p.MarginPct != 0 {
		t.Fatalf("project level not cost-only: %+v", p)
	}
	r := p.Resources[0]
	if r.TotalCost != 100 || r.Revenue != 0 || r.Margin != 0 || r.MarginPct != 0 {
		t.Fatalf("resource level not cost-only: %+v", r)
	}
}

func TestRollupClientIncludesAgentInSubtotals(t *testing.T) {
	cl := &adminCostClient{
		ClientID: "u-1", ClientName: "acme@example.com",
		Projects: []adminCostProject{{
			ProjectID: "p-acme", ProjectName: "Acme",
			Resources: []adminCostResource{
				{Name: "web", Kind: "app", TotalCost: 100, Revenue: 150},
				{Name: agentResourceKind, Kind: agentResourceKind, TotalCost: 30, Revenue: 80},
			},
		}},
	}
	rollupClient(cl)

	p := cl.Projects[0]
	if p.Cost != 130 || p.Revenue != 230 {
		t.Fatalf("project subtotal must include every resource: got cost %v revenue %v, want 130/230", p.Cost, p.Revenue)
	}
	if cl.Cost != 130 || cl.Revenue != 230 {
		t.Fatalf("client subtotal must include every resource: got cost %v revenue %v, want 130/230", cl.Cost, cl.Revenue)
	}

	var agent *adminCostResource
	for i := range p.Resources {
		if p.Resources[i].Kind == agentResourceKind {
			agent = &p.Resources[i]
		}
	}
	if agent == nil {
		t.Fatal("agent row dropped: it must still render as a resource")
	}
	if agent.TotalCost != 30 || agent.Revenue != 80 || agent.Margin != 50 {
		t.Fatalf("agent row must carry its own priced cost/revenue/margin: got %+v, want cost 30 revenue 80 margin 50", *agent)
	}
}

func TestMarginPct(t *testing.T) {
	cases := []struct {
		name    string
		revenue float64
		cost    float64
		want    float64
	}{
		{"zero revenue yields zero", 0, 50, 0},
		{"negative revenue guarded", -10, 5, 0},
		{"healthy margin", 200, 50, 75},
		{"loss below cost", 100, 150, -50},
		{"break even", 100, 100, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := marginPct(tc.revenue, tc.cost); got != tc.want {
				t.Fatalf("marginPct(%v, %v) = %v, want %v", tc.revenue, tc.cost, got, tc.want)
			}
		})
	}
}
