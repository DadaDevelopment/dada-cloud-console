/**
 * Shared "deploy from your own CI" snippet builders. Used by the app detail
 * page's DeployHooksCard (after a deploy token is created) and by the
 * git-import wizard's GitHub Actions callout (before any token exists, as a
 * guide preview).
 */

/**
 * A single GitHub Actions workflow step that pushes a new image via a Dada
 * Cloud deploy token.
 */
export function githubActionsStep(): string {
  return [
    "- name: Deploy to Dada Cloud",
    "  uses: dada-tuda/deploy-action@v1",
    "  with:",
    "    token: ${{ secrets.DADA_DEPLOY_TOKEN }}",
    "    image: ghcr.io/OWNER/REPO:${{ github.sha }}",
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
