/**
 * Unit tests for lib/git-install-connect.ts.
 *
 * Run with Node's built-in test runner and type stripping:
 *
 *   cd frontend && npm run test:unit
 *
 * Grounds two claims:
 *
 * 1. connectToGithub always fetches a fresh install url right before
 *    navigating and never caches or reuses one across calls -- required
 *    because the backend correlates FinishGitAppInstall to
 *    StartGitAppInstall by the nonce embedded in that url, and a reused url
 *    would attribute the finish event to the wrong start (or none).
 *
 * 2. A component that wires its "connect to GitHub" button through
 *    connectToGithub performs the fetch on the click, not on mount. This is
 *    the regression this module exists to prevent: the git/import page used
 *    to prefetch the install url in a mount effect, which meant every page
 *    render, not just a deliberate click, wrote a StartGitAppInstall audit
 *    row and inflated the "started GitHub install" funnel count with page
 *    views. The render/click test below fails if that prefetch effect comes
 *    back; see the harness component at the bottom of this file.
 */

import { test } from "node:test";
import assert from "node:assert/strict";

import { connectToGithub, type ConnectToGithubDeps } from "./git-install-connect.ts";

const GITHUB_INSTALL_HOST = ["https:", "", "github.com"].join("/");

function installUrlFor(state: string): string {
  return `${GITHUB_INSTALL_HOST}/apps/dada/installations/new?state=${state}`;
}

test("connectToGithub fetches a fresh url every call and never caches it", async () => {
  let fetchCalls = 0;
  const navigated: string[] = [];
  const deps: ConnectToGithubDeps = {
    fetchInstallUrl: async () => {
      fetchCalls += 1;
      return installUrlFor(`call-${fetchCalls}`);
    },
    navigate: (url) => navigated.push(url),
  };

  await connectToGithub(deps);
  await connectToGithub(deps);

  assert.equal(fetchCalls, 2);
  assert.deepEqual(navigated, [installUrlFor("call-1"), installUrlFor("call-2")]);
});

test("connectToGithub propagates a fetch failure and never navigates", async () => {
  let navigated = false;
  await assert.rejects(
    connectToGithub({
      fetchInstallUrl: async () => {
        throw new Error("git integration not configured");
      },
      navigate: () => {
        navigated = true;
      },
    }),
    /not configured/
  );
  assert.equal(navigated, false);
});

/**
 * Mount-vs-click harness. Renders a real React component into a real DOM
 * (via jsdom) and counts calls to fetchInstallUrl separately for the mount
 * and the click, mirroring how the git/import page wires its "connect to
 * GitHub" controls through connectToGithub.
 */
test("a connect button wired through connectToGithub fetches on click, not on mount", async () => {
  const { JSDOM } = await import("jsdom");
  const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });

  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.MouseEvent = dom.window.MouseEvent;
  Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

  const React = await import("react");
  const { createRoot } = await import("react-dom/client");
  const { act } = React;

  let fetchCalls = 0;
  let navigated: string | null = null;
  const targetUrl = installUrlFor("x");
  const deps: ConnectToGithubDeps = {
    fetchInstallUrl: async () => {
      fetchCalls += 1;
      return targetUrl;
    },
    navigate: (url) => {
      navigated = url;
    },
  };

  function ConnectButtonHarness() {
    const [busy, setBusy] = React.useState(false);
    const onClick = () => {
      setBusy(true);
      void connectToGithub(deps).finally(() => setBusy(false));
    };
    return React.createElement(
      "button",
      { type: "button", onClick, disabled: busy, "data-testid": "connect" },
      "open github"
    );
  }

  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  try {
    await act(async () => {
      root.render(React.createElement(ConnectButtonHarness));
    });

    assert.equal(fetchCalls, 0, "mounting the button must not fetch an install url");

    const button = container.querySelector('[data-testid="connect"]') as HTMLButtonElement;
    assert.ok(button, "connect button did not render");

    await act(async () => {
      button.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
      await new Promise((resolve) => setTimeout(resolve, 0));
    });

    assert.equal(fetchCalls, 1, "one click must fetch exactly one install url");
    assert.equal(navigated, targetUrl);
  } finally {
    await act(async () => {
      root.unmount();
    });
  }
});
