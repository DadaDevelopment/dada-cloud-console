/**
 * Shared "deploy from your own CI" snippet builders. Used by the app detail
 * page's DeployHooksCard (after a deploy token is created) and by the
 * git-import wizard's GitHub Actions callout (before any token exists, as a
 * guide preview).
 */

/**
 * A single GitHub Actions workflow step that deploys a prebuilt image via a
 * Dada Cloud deploy token. Self-contained (plain curl, no external action), so
 * it works in any repo without depending on a published marketplace action.
 * The token is passed through the step env, keeping it out of the process
 * argument list. `baseUrl` is the console API origin.
 */
export function githubActionsStep(baseUrl: string): string {
  return [
    "- name: Deploy to Dada Cloud",
    "  env:",
    "    DADA_DEPLOY_TOKEN: ${{ secrets.DADA_DEPLOY_TOKEN }}",
    "  run: |",
    `    curl -fsS -X POST ${baseUrl}/api/v1/deploy \\`,
    '      -H "Authorization: Bearer $DADA_DEPLOY_TOKEN" \\',
    '      -H "Content-Type: application/json" \\',
    '      -d "{\\"image\\":\\"ghcr.io/OWNER/REPO:${{ github.sha }}\\"}"',
  ].join("\n");
}

/**
 * Plain curl equivalent of {@link githubActionsStep}, for CI systems other
 * than GitHub Actions. `baseUrl` is the console API origin.
 */
export function deployCurl(baseUrl: string): string {
  return [
    `curl -fsS -X POST ${baseUrl}/api/v1/deploy \\`,
    `  -H "Authorization: Bearer $DADA_DEPLOY_TOKEN" \\`,
    `  -H "Content-Type: application/json" \\`,
    `  -d '{"image":"ghcr.io/OWNER/REPO:'"$GITHUB_SHA"'"}'`,
  ].join("\n");
}
