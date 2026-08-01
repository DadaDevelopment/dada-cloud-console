# Add a "Deploy on Dada" button to your repository

## What it's for

You maintain a public GitHub repository — a starter template, a bot, a demo, an open-source
tool — and you want anyone reading it to be able to run it without a terminal. A "Deploy on
Dada" badge in your README does exactly that: one click builds the repository and deploys it
into the reader's own Dada Cloud account.

The reader does not need to own or fork the repository, does not need to install a GitHub App,
and does not need to fill in a wizard. This is the same idea as the "Deploy on Railway" and
"Deploy to Render" buttons, with the deploy happening on a Russian platform paid for in rubles.

## How to add the button

1. Open the app in the console, or just take the snippet below and replace `OWNER/REPO` with
   your repository path (for example `DadaDevelopment/dadatuda-starter`).
2. Paste the Markdown into your `README.md`:

```markdown
[![Deploy on Dada](https://console.dada-tuda.ru/deploy-button.svg)](https://console.dada-tuda.ru/deploy?repo=OWNER/REPO)
```

3. If your docs site does not render Markdown, use the HTML flavour instead:

```html
<a href="https://console.dada-tuda.ru/deploy?repo=OWNER/REPO">
  <img src="https://console.dada-tuda.ru/deploy-button.svg" alt="Deploy on Dada" height="40">
</a>
```

4. Commit and push. The badge is live immediately — nothing to register, no marketplace
   listing, no approval step.

## Getting the snippet from the console

If the repository is already linked to an app, the console writes the snippet for you:

1. Open **Applications** and click the app.
2. Scroll to the **"Deploy on Dada" button** card.
3. Copy either the Markdown or the HTML snippet — both are pre-filled with your repository.

## Options

The link accepts a few query parameters:

- `repo` — required, `owner/name`. A full GitHub URL (`https://github.com/owner/name`) or an
  SSH remote also works; a trailing `.git` is stripped.
- `branch` — the branch to build. Defaults to `main`.
- `rootDir` — the subdirectory to build, for monorepos. Defaults to the repository root.

A monorepo example:

```markdown
[![Deploy on Dada](https://console.dada-tuda.ru/deploy-button.svg)](https://console.dada-tuda.ru/deploy?repo=OWNER/REPO&branch=develop&rootDir=services/api)
```

## What the reader sees

1. They click the badge and land on a confirmation card showing the repository, the detected
   framework, the application name, and the port.
2. If they are not signed in, registration opens first and returns them to the same card.
3. They can rename the app or change the port, then click **Deploy**.
4. The build log streams on the deployments page, and the app gets an HTTPS address of the form
   `name-hash.dada-tuda.ru` when the build finishes.

## Gotchas

- **Public repositories only.** The clone is anonymous, so a private repository will fail to
  build. For private code, connect GitHub in the console instead — see
  [Deploy an app from GitHub](applications-deploy-from-github.md).
- **Framework detection is best-effort.** It reads the repository through GitHub's anonymous
  API, which is rate-limited per source address. Results are cached for six hours. If detection
  is unavailable at that moment, the port simply defaults to `8080` and the build still runs —
  the reader can correct the port before deploying, or in app settings afterwards.
- **Auto-deploy is off** for apps created this way. The reader is not the repository owner, so
  the app is a snapshot of your code at click time, not a subscription to your pushes. They can
  connect their own fork later if they want push-to-deploy.
- **Name collisions get a suffix.** If the reader's project already has an app with that name,
  a short random suffix is appended rather than overwriting or rebuilding the existing app.
- **The badge image is served from the console**, so it renders anywhere — GitHub, GitLab, a
  docs site, a blog post.

## Not yet supported

- No pre-filled environment variables or a `dada.json`-style manifest: the reader configures
  secrets themselves after the first deploy.
- No "deploy a subdirectory as several apps" — one click creates one application.
- No custom badge styling or a per-repository badge with your own colors.
