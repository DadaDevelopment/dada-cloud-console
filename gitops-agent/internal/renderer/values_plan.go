package renderer

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// ValuesPlan is what a deploy would do to values.yaml, expressed as paths.
//
// It exists so a caller can be shown the effect of a write BEFORE the write:
// the console renders values.yaml from its database, and an app assembled by
// hand in git carries keys the database has never heard of. Removed is the
// dangerous list — those are the keys that exist only in git.
//
// Paths are named the way the clobber guard names them, so a plan and a refusal
// speak about the same thing: a list of maps carrying a "name" is indexed by
// that name, e.g. common.extraEnv.PGHOST.
type ValuesPlan struct {
	Added   []string `json:"added"`
	Changed []string `json:"changed"`
	Removed []string `json:"removed"`
}

// Empty reports whether the deploy would leave the file exactly as it is.
func (p ValuesPlan) Empty() bool {
	return len(p.Added) == 0 && len(p.Changed) == 0 && len(p.Removed) == 0
}

// PlanValuesChange diffs the file in git against the file a deploy would write.
//
// It takes the MERGED result rather than the raw render, for the same reason
// the clobber guard does: the merge preserves every key the console has no
// opinion about, so diffing the raw render reports losses that would never
// happen and buries the ones that would.
//
// A parse failure on either side yields an empty plan. A plan is an explanation,
// and an explanation invented from a file that could not be read is worse than
// none.
func PlanValuesChange(existingYAML, mergedYAML string) ValuesPlan {
	var existing, merged any
	if err := yaml.Unmarshal([]byte(existingYAML), &existing); err != nil {
		return ValuesPlan{}
	}
	if err := yaml.Unmarshal([]byte(mergedYAML), &merged); err != nil {
		return ValuesPlan{}
	}

	plan := ValuesPlan{Added: []string{}, Changed: []string{}, Removed: []string{}}
	collectDropped("", existing, merged, &plan.Removed)
	collectDropped("", merged, existing, &plan.Added)
	collectChanged("", existing, merged, &plan.Changed)
	sort.Strings(plan.Added)
	sort.Strings(plan.Changed)
	sort.Strings(plan.Removed)
	return plan
}

// collectChanged walks two decoded trees in parallel and appends the path of
// every leaf that exists on both sides with a different value. Absences are
// left to collectDropped, so a path never appears in two lists of one plan.
func collectChanged(path string, before, after any, out *[]string) {
	switch beforeVal := before.(type) {
	case map[string]any:
		afterMap, ok := after.(map[string]any)
		if !ok {
			*out = append(*out, pathOr(path))
			return
		}
		for k, v := range beforeVal {
			if child, exists := afterMap[k]; exists {
				collectChanged(joinPath(path, k), v, child, out)
			}
		}
	case []any:
		afterList, ok := after.([]any)
		if !ok {
			*out = append(*out, pathOr(path))
			return
		}
		beforeNamed, _ := splitNamed(beforeVal)
		afterNamed, _ := splitNamed(afterList)
		for name, v := range beforeNamed {
			if child, exists := afterNamed[name]; exists {
				collectChanged(joinPath(path, name), v, child, out)
			}
		}
	default:
		if fmt.Sprint(before) != fmt.Sprint(after) {
			*out = append(*out, pathOr(path))
		}
	}
}
