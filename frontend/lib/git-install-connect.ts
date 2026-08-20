/**
 * The single place that turns "user wants to go install our GitHub App" into
 * an actual browser navigation.
 *
 * `StartGitAppInstall` (backend, GET .../git/install-url) writes an audit row
 * every time this fires, carrying the nonce `FinishGitAppInstall` later
 * correlates against. That makes the contract load-bearing: this must run
 * exactly once per real click, fetching a FRESH url immediately before
 * navigating. A cached/reused url would carry a nonce recorded against a
 * different (or no) click, so `FinishGitAppInstall` would correlate against
 * the wrong intent and the install funnel derived from these events would be
 * measuring something else. Never memoize the url returned here.
 */
export interface ConnectToGithubDeps {
  fetchInstallUrl: () => Promise<string>;
  navigate: (url: string) => void;
}

export async function connectToGithub(deps: ConnectToGithubDeps): Promise<void> {
  const url = await deps.fetchInstallUrl();
  deps.navigate(url);
}
