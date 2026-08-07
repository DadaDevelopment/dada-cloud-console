/**
 * Names for the `data-ux` markers the console puts on its first-run controls.
 *
 * `lib/ux-telemetry.ts` prefers an explicit `data-ux` over anything it can read
 * off the page, so these strings ARE the buckets clicks land in — which makes
 * them worth building in one place instead of interpolating at each call site.
 */

/**
 * Where a template-deploy block is standing. All three placements render the
 * same CTA words, so without this they collapse into one bucket
 * (`button:Развернуть`) and "which onramp actually starts deploys" has no
 * answer. The layout props are not a substitute: `compact` and `hero` say how
 * the block looks, not which screen the person is on.
 */
export type TemplateDeployPlacement = "onramp" | "apps-empty" | "git-import";

/**
 * Builds the marker for one control inside a template-deploy block.
 *
 * The placement always leads, so one screen's numbers can be read without the
 * others, and only catalog-controlled words follow it. A pasted or searched
 * repository name never appears: it is visitor-supplied, would put every row in
 * its own bucket, and telemetry is the wrong column for it under 152-FZ.
 *
 * Empty parts are dropped rather than joined, so an optional segment cannot
 * leave a double dot that reads as a missing level.
 */
export function templateUxName(placement: TemplateDeployPlacement, ...parts: string[]): string {
  return ["tpl", placement, ...parts.filter(Boolean)].join(".");
}
