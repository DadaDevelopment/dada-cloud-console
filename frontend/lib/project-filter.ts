import type { Project } from "./types";

/**
 * Projects split into the two groups the switcher renders: the ones running
 * something, and the ones holding nothing.
 */
export interface PartitionedProjects {
  populated: Project[];
  empty: Project[];
}

/**
 * Filters projects by a free-text query and splits them into populated and
 * empty groups, each ordered by app count and then name.
 *
 * A platform admin sees every project on the platform, and most of them are
 * short-lived agent and e2e leftovers with zero apps. A flat, name-sorted,
 * unfilterable list buried the handful of projects that actually run something.
 * Empty projects are sunk rather than hidden: a project the user just created is
 * empty too, and hiding it would read as a failed create.
 */
export function partitionProjects(projects: Project[], filter: string): PartitionedProjects {
  const q = filter.trim().toLowerCase();
  const matched = q
    ? projects.filter(
        (p) =>
          p.display_name.toLowerCase().includes(q) || p.name.toLowerCase().includes(q),
      )
    : projects;
  const byApps = (a: Project, b: Project) =>
    (b.app_count ?? 0) - (a.app_count ?? 0) || a.name.localeCompare(b.name);
  return {
    populated: matched.filter((p) => (p.app_count ?? 0) > 0).sort(byApps),
    empty: matched.filter((p) => (p.app_count ?? 0) === 0).sort(byApps),
  };
}
