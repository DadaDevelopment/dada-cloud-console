// Command boxcost prints the derived Dada Box unit economics as JSON and exits.
//
// It exists for the same reason cmd/migrate does: a caller that must not have to
// boot the whole server to get one answer. Here the caller is
// scripts/box-unit-cost-check.sh, which reconciles our derived price against the
// cluster's real measured cost and alerts on a negative margin.
//
// Everything it prints is DERIVED, never declared (decision D5): the unit costs come
// from billing/data/box-fleet-cost.yaml through costengine.ComputeUnitCost, the
// per-minute figure is that monthly cost divided by exactly
// costengine.MinutesPerMonth, and the customer-facing price applies
// pricing.MarkupDefault. There is no price table anywhere for this tool to read, and
// that is the point — a second source of truth for a per-minute price is how the
// "matches a VPS at 43200 minutes" claim would quietly stop being true.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dada-tuda/console/backend/internal/billing"
	"github.com/dada-tuda/console/backend/internal/billing/costengine"
	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/boxcatalog"
)

// profileCost is one catalog size, priced.
//
// InternalRubHour and PriceRubHour are reported separately on purpose: the margin
// check needs both terms, and a tool that emitted only the customer-facing price
// would force its caller to re-derive the internal one — which is exactly the second
// implementation this file exists to avoid.
type profileCost struct {
	Profile          string  `json:"profile"`
	VCPU             int     `json:"vcpu"`
	RAMGB            float64 `json:"ram_gb"`
	DiskGB           int     `json:"disk_gb"`
	InternalRubMonth float64 `json:"internal_rub_month"`
	InternalRubHour  float64 `json:"internal_rub_hour"`
	PriceRubMonth    float64 `json:"price_rub_month"`
	PriceRubMinute   float64 `json:"price_rub_minute"`
	PriceRubHour     float64 `json:"price_rub_hour"`
	// SleepingRubHour is the storage-only accrual of the same box while asleep. Its
	// presence in this output is deliberate: "idle is free" is only honest because
	// this number exists and is billed separately.
	SleepingRubHour float64 `json:"sleeping_rub_hour"`
}

type output struct {
	MinutesPerMonth int     `json:"minutes_per_month"`
	Markup          float64 `json:"markup"`
	UnitCost        struct {
		PerVCPURubMonth      float64 `json:"per_vcpu_rub_month"`
		PerGBRAMRubMonth     float64 `json:"per_gb_ram_rub_month"`
		PerGBStorageRubMonth float64 `json:"per_gb_storage_rub_month"`
	} `json:"unit_cost"`
	Profiles []profileCost `json:"profiles"`
}

func main() {
	fleet, err := billing.LoadBoxFleetCost("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "boxcost: load box fleet cost: %v\n", err)
		os.Exit(1)
	}
	unit, err := costengine.ComputeUnitCost(fleet)
	if err != nil {
		// Fails closed rather than printing zeros: a zero unit cost would report an
		// infinite margin and a free box, both of which look fine on a dashboard.
		fmt.Fprintf(os.Stderr, "boxcost: derive unit cost: %v\n", err)
		os.Exit(1)
	}

	out := output{MinutesPerMonth: costengine.MinutesPerMonth, Markup: pricing.MarkupDefault}
	out.UnitCost.PerVCPURubMonth = unit.PerVCPU
	out.UnitCost.PerGBRAMRubMonth = unit.PerGBRAM
	out.UnitCost.PerGBStorageRubMonth = unit.PerGBStorage

	for _, s := range boxcatalog.V1Sizes {
		active := costengine.Footprint{
			VCPU:      float64(s.VCPU),
			RAMGB:     float64(s.MemoryMB) / 1024,
			StorageGB: float64(s.DiskGB),
		}
		asleep := costengine.Footprint{StorageGB: float64(s.DiskGB)}
		internalMinute := costengine.PerMinuteCost(active, unit)
		priceMinute := internalMinute * pricing.MarkupDefault
		out.Profiles = append(out.Profiles, profileCost{
			Profile:          s.Name,
			VCPU:             s.VCPU,
			RAMGB:            float64(s.MemoryMB) / 1024,
			DiskGB:           s.DiskGB,
			InternalRubMonth: costengine.PlanCost(active, unit),
			InternalRubHour:  internalMinute * 60,
			PriceRubMonth:    costengine.PlanCost(active, unit) * pricing.MarkupDefault,
			PriceRubMinute:   priceMinute,
			PriceRubHour:     priceMinute * 60,
			SleepingRubHour:  costengine.PerMinuteCost(asleep, unit) * pricing.MarkupDefault * 60,
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "boxcost: encode: %v\n", err)
		os.Exit(1)
	}
}
