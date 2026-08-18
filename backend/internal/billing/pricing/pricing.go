package pricing

import (
	"fmt"
	"strings"

	"github.com/dada-tuda/console/backend/internal/billing/costengine"
)

// MarkupDefault is the multiplier applied to the internal per-unit cost to
// derive the customer-facing (informational) price.
const MarkupDefault = 1.5

const markupDefault = MarkupDefault

// Quotas holds the per-plan resource limits.
// A value of 0 means unlimited (used for Enterprise).
//
// The json tags are load-bearing, not decoration: this struct is serialized
// straight to the console by GET /billing/plans and by the "quotas" block of
// GET /projects/{id}/billing/account. Without them Go emits Go field names and
// every consumer that reads price_rub / quotas.apps sees undefined -- which is
// exactly how the quota upsell lost its ability to name a plan. Key names match
// the resource vocabulary used everywhere else in billing (pricing.Quota, the
// usage map, the 403 payload), so "team_members" is spelled the same on every
// surface.
type Quotas struct {
	Apps                int `yaml:"apps" json:"apps"`
	Databases           int `yaml:"databases" json:"databases"`
	StorageGB           int `yaml:"storage_gb" json:"storage_gb"`
	Domains             int `yaml:"domains" json:"domains"`
	Environments        int `yaml:"environments" json:"environments"`
	TeamMembers         int `yaml:"team_members" json:"team_members"`
	BackupRetentionDays int `yaml:"backup_retention_days" json:"backup_retention_days"`
	// BoxMinutes is the per-CALENDAR-MONTH allowance of billed active box
	// minutes. Unlike every other quota in this struct it is a flow, not a
	// stock: apps/databases/domains count what exists right now, box minutes
	// count what was consumed since the first of the month.
	//
	// That difference is the whole reason a box needs its own quota. Counting
	// live boxes would cap concurrency, and a free-tier account that keeps one
	// box awake for 30 days would pass such a gate while consuming 43200
	// minutes. The gate has to be on the metered flow or it does not bound
	// anything (see boxMinutesQuotaResource in internal/api/billing.go).
	BoxMinutes int `yaml:"box_minutes" json:"box_minutes"`

	AppServers int `yaml:"app_servers" json:"app_servers"`
}

// Plan represents a billing plan loaded from plans.yaml.
//
// InternalFootprint is dropped from JSON on purpose. It is the input to
// PriceFloor, an internal number that must never reach a customer-facing
// surface, and it was being handed out with every plan listing.
type Plan struct {
	Key               string               `yaml:"key" json:"key"`
	Name              string               `yaml:"name" json:"name"`
	PriceRUB          float64              `yaml:"price_rub" json:"price_rub"`
	Quotas            Quotas               `yaml:"quotas" json:"quotas"`
	Capabilities      []string             `yaml:"capabilities" json:"capabilities"`
	SupportLevel      string               `yaml:"support_level" json:"support_level"`
	InternalFootprint costengine.Footprint `yaml:"internal_footprint" json:"-"`
}

// Need describes what a user requires to find a recommended plan.
//
// The json tags carry the same weight as those on Quotas: this struct is the
// bound body of POST /billing/recommend-plan, which the pricing page posts in
// snake_case. Untagged, every field bound to zero and the recommender answered
// "free" to a customer who had just described a business-sized need.
type Need struct {
	Apps      int `json:"apps"`
	Databases int `json:"databases"`
	Domains   int `json:"domains"`
	Members   int `json:"members"`
	StorageGB int `json:"storage_gb"`
}

// PriceFloor returns the minimum published price for a plan based on its
// internal cost and the default markup. This is an internal value and must
// never appear in customer-facing surfaces.
func PriceFloor(p Plan, u costengine.UnitCost) float64 {
	return costengine.PlanCost(p.InternalFootprint, u) * markupDefault
}

// IncludedConsumptionRub is how much list-price consumption one calendar month
// of a plan buys before the account is over-consuming what it pays for.
//
// It is max(published price, price floor) rather than the published price
// alone, because the free plan's price is zero and a zero allowance would
// declare every free account in overage the moment it deploys anything. The
// floor is the plan's own internal footprint priced at the real cluster unit
// cost with the standard markup -- that IS what a free account was budgeted to
// consume, so the free tier gets a real allowance without anyone inventing a
// number for it. For paid plans the published price is always the higher of the
// two (the margin guard enforces exactly that), so they are measured against
// the money the customer actually handed over.
//
// Zero means "no allowance is defined": enterprise carries a negotiated
// contract with zero quotas and zero footprint here, and a per-contract
// allowance cannot be derived from plans.yaml. Callers must skip those rather
// than treat zero as a limit, or every enterprise account alerts forever.
func IncludedConsumptionRub(p Plan, u costengine.UnitCost) float64 {
	floor := PriceFloor(p, u)
	if p.PriceRUB > floor {
		return p.PriceRUB
	}
	return floor
}

// RecommendPlan returns the cheapest plan whose quotas all satisfy the need.
// Enterprise is the catch-all when no other plan fits. The reason string
// lists the driving constraint(s).
// Plans must be ordered from cheapest to most expensive (free → startup → business → enterprise).
func RecommendPlan(req Need, plans []Plan) (Plan, string) {
	for _, p := range plans {
		if p.Key == "enterprise" {
			continue
		}
		reasons := planShortfalls(p, req)
		if len(reasons) == 0 {
			return p, fmt.Sprintf("fits within %s quotas", p.Name)
		}
	}

	for _, p := range plans {
		if p.Key == "enterprise" {
			return p, "need exceeds all standard plans: " + strings.Join(enterpriseReasons(req), ", ")
		}
	}

	if len(plans) > 0 {
		return plans[len(plans)-1], "fallback: enterprise catch-all"
	}
	return Plan{}, "no plans available"
}

// Quota returns the limit for a countable resource by name.
// For Enterprise (key="enterprise") all quotas are 0, which means unlimited.
// Returns (limit, true) when the resource name is known, (0, false) otherwise.
func Quota(p Plan, resource string) (int, bool) {
	switch resource {
	case "apps":
		return p.Quotas.Apps, true
	case "databases":
		return p.Quotas.Databases, true
	case "domains":
		return p.Quotas.Domains, true
	case "team_members":
		return p.Quotas.TeamMembers, true
	case "storage_gb":
		return p.Quotas.StorageGB, true
	case "box_minutes":
		return p.Quotas.BoxMinutes, true
	case "app_servers":
		return p.Quotas.AppServers, true
	}
	return 0, false
}

func planShortfalls(p Plan, req Need) []string {
	var out []string
	if p.Quotas.Apps > 0 && req.Apps > p.Quotas.Apps {
		out = append(out, fmt.Sprintf("apps(%d>%d)", req.Apps, p.Quotas.Apps))
	}
	if p.Quotas.Databases > 0 && req.Databases > p.Quotas.Databases {
		out = append(out, fmt.Sprintf("databases(%d>%d)", req.Databases, p.Quotas.Databases))
	}
	if p.Quotas.Domains > 0 && req.Domains > p.Quotas.Domains {
		out = append(out, fmt.Sprintf("domains(%d>%d)", req.Domains, p.Quotas.Domains))
	}
	if p.Quotas.TeamMembers > 0 && req.Members > p.Quotas.TeamMembers {
		out = append(out, fmt.Sprintf("team_members(%d>%d)", req.Members, p.Quotas.TeamMembers))
	}
	if p.Quotas.StorageGB > 0 && req.StorageGB > p.Quotas.StorageGB {
		out = append(out, fmt.Sprintf("storage_gb(%d>%d)", req.StorageGB, p.Quotas.StorageGB))
	}
	return out
}

func enterpriseReasons(req Need) []string {
	return []string{
		fmt.Sprintf("apps=%d", req.Apps),
		fmt.Sprintf("databases=%d", req.Databases),
		fmt.Sprintf("domains=%d", req.Domains),
		fmt.Sprintf("members=%d", req.Members),
		fmt.Sprintf("storage_gb=%d", req.StorageGB),
	}
}
