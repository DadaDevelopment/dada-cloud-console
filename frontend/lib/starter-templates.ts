/**
 * Single source of truth for the platform's starter templates: the repos the
 * no-git "deploy a template" escape hatch (components/console/template-deploy-cards.tsx)
 * points `gitApi.linkRepo` at. Previously this list was duplicated inline in
 * that component; anything that needs to recognize "this app is still running
 * the demo, not the user's own code" (e.g. the starter-next-step panel) needs
 * the same list, so it lives here once.
 */
export interface StarterTemplate {
  key: string;
  repo_full_name: string;
  port: number;
}

export const STARTER_TEMPLATES: StarterTemplate[] = [
  { key: "nextjs", repo_full_name: "DadaDevelopment/dada-nextjs-starter", port: 3000 },
  { key: "fastapi", repo_full_name: "DadaDevelopment/dada-fastapi-starter", port: 8000 },
  { key: "static", repo_full_name: "DadaDevelopment/dada-static-starter", port: 8080 },
];

/**
 * True when `repoFullName` names one of the starter templates above,
 * case-insensitively. Used to detect an app that is still running the demo
 * repo verbatim, so the product can nudge toward connecting the user's own
 * code instead of the sample.
 */
export function isStarterRepo(repoFullName: string | undefined | null): boolean {
  if (!repoFullName) return false;
  const normalized = repoFullName.trim().toLowerCase();
  if (!normalized) return false;
  return STARTER_TEMPLATES.some((tpl) => tpl.repo_full_name.toLowerCase() === normalized);
}
