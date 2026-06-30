package pricing

import (
	"fmt"
	"strings"

	"github.com/dada-tuda/console/backend/internal/billing/costengine"
)

const markupDefault = 2.7

// Quotas holds the per-plan resource limits.
// A value of 0 means unlimited (used for Enterprise).
type Quotas struct {
	Apps                 int `yaml:"apps"`
	Databases            int `yaml:"databases"`
	StorageGB            int `yaml:"storage_gb"`
	Domains              int `yaml:"domains"`
	Environments         int `yaml:"environments"`
	TeamMembers          int `yaml:"team_members"`
	BackupRetentionDays  int `yaml:"backup_retention_days"`
}

// Plan represents a billing plan loaded from plans.yaml.
type Plan struct {
	Key               string                    `yaml:"key"`
	Name              string                    `yaml:"name"`
	PriceRUB          float64                   `yaml:"price_rub"`
	Quotas            Quotas                    `yaml:"quotas"`
	Capabilities      []string                  `yaml:"capabilities"`
	SupportLevel      string                    `yaml:"support_level"`
	InternalFootprint costengine.Footprint      `yaml:"internal_footprint"`
}

// Need describes what a user requires to find a recommended plan.
type Need struct {
	Apps      int
	Databases int
	Domains   int
	Members   int
	StorageGB int
}

// PriceFloor returns the minimum published price for a plan based on its
// internal cost and the default markup. This is an internal value and must
// never appear in customer-facing surfaces.
func PriceFloor(p Plan, u costengine.UnitCost) float64 {
	return costengine.PlanCost(p.InternalFootprint, u) * markupDefault
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
