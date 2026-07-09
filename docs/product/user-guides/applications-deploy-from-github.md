# Deploy an application from GitHub

> **Doc-vs-code note:** `docs/product/feature-inventory.md` says the three deploy paths are
> "collapsed behind one button with no explanation of the difference." That's out of date —
> the current **Deploy application** dialog already shows three explicit, labeled cards
> (**From GitHub**, **From image**, **From Compose**), each with its own description. The
> inventory should be updated to reflect this.

## What it's for

Push code to GitHub, get a live URL. Connect a repository once and every `git push` to your
production branch triggers a new build and deploy automatically — no Dockerfile required for
most common frameworks (Next.js, FastAPI, Spring Boot, Django, Go, and 15+ others are
auto-detected).

## How to deploy

1. Open your project and go to **Applications**.
2. Click **Deploy application**.
3. Choose the **Environment** you're deploying into (Cloud or VM — VM environments cannot
   deploy this way, see Gotchas).
4. Pick the **From GitHub** card, then click **Continue**.
5. If you haven't connected a GitHub account yet, click **Connect GitHub** and authorize the
   dada-cloud GitHub App. You can connect additional accounts/orgs later with
   **Add another account**.
6. Search and pick the repository you want to deploy. Private repos show a lock icon.
7. Review the **Configure** step:
   - **Application name** — pre-filled from the repo name, editable (lowercase letters,
     digits, hyphens).
   - **Framework** — auto-detected from your repo; override it from the dropdown if it
     guessed wrong (grouped by Java/JVM, Python, JavaScript/TypeScript, Static/Dockerfile).
   - **Port**, **Profile** (small/medium/large), **Production branch**, **Root directory**.
   - **Auto-deploy** toggle — on by default; every push to the production branch redeploys.
8. Click **Deploy**. The wizard streams the build log live on the same page.
9. When the build succeeds you get **Retry**, **View deployments**, and **Open app** actions.

## Gotchas

- Changing the **Root directory** re-runs framework auto-detection automatically (on blur).
- If auto-detect can't identify anything, port defaults to `8080` and you pick the framework
  manually.
- Once you click Deploy, the source/repo pick and Configure fields lock — you can't edit the
  spec the build was triggered with mid-flight. Fix mistakes by finishing the flow and editing
  app settings afterward, or `git import` again.
- A repo can only be linked to one app at a time; re-linking the same repo/app pair is treated
  as "already connected" and just proceeds to a new deploy, it won't error.
- The **Builds** nav item (repo connections + history) is only visible to Owner/Admin roles.
  A Developer can still deploy through this wizard (it only requires write access), but won't
  see the Builds list afterward — use the app's **Deployments** tab instead, which every
  Developer can reach.
- VM environments show a warning banner and block deployment from this page entirely — use
  [Bring your own VM](app-servers-bring-your-own-vm.md) or
  [Adopt existing workloads](app-servers-adopt-existing-workloads.md) instead.

## Not yet supported

- No preview/staging deploys per pull request — only the one configured production branch
  auto-deploys.
- No build cache configuration.
- No way to change the port/profile/framework override after the initial import from this
  wizard's UI — those live in app settings once the app exists.
