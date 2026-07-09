# Deploy a Docker image, or bring a Compose stack

> **Doc-vs-code note:** the inventory doc claims deploy paths are "collapsed behind one
> button." In the current UI, **Deploy application** already opens three explicit cards —
> **From GitHub**, **From image**, **From Compose** — each with its own description. It also
> claims the **Adopt** action "has no label saying what it does" — the button is actually
> labeled **"Split into applications"** with a tooltip explaining the effect. Both claims need
> correcting in the inventory.

## What it's for

- **From image**: you already have a container image in a registry (Docker Hub, GHCR, your
  own) and just want it running behind a URL, with metrics/logs/env vars — no build step.
- **From Compose**: you have an existing `docker-compose.yaml` stack (e.g. a legacy app you're
  migrating) and want each of its services to become its own managed Dada application.

## Deploy a prebuilt image

1. Go to **Applications** → **Deploy application**.
2. Choose the environment, pick the **From image** card, click **Continue**.
3. Fill in the **Create Application** form:
   - **Name** (Kubernetes resource name — lowercase letters, digits, hyphens).
   - **Image** (e.g. `ghcr.io/you/app:latest`).
   - **Port**, **Replicas**, **Profile**.
4. Click **Create App**.
5. To ship a new version later, open the app → **Deploy Image**, enter the new tag, and
   deploy — the current tag is shown for reference.

## Bring an existing Compose stack ("From Compose")

There is no direct "paste a compose.yaml" form on the Applications page. Clicking **From
Compose** in the Deploy dialog takes you straight to **App Servers**, because compose stacks
only enter Dada through VM adoption:

1. [Connect a VM](app-servers-bring-your-own-vm.md) (or [order one](app-servers-order-a-vm.md))
   that's already running your compose stack, and wait for it to reach **Ready**.
2. Open the server → **Workloads** tab → **Discover workload** to inventory the running
   containers (read-only, via Portainer, no SSH).
3. Click **Import into Dada** (see
   [Adopt existing workloads](app-servers-adopt-existing-workloads.md) for the full wizard).

Each service you select becomes its **own** Dada application, with its own logs, metrics, and
settings — not one combined app. Named volumes are re-attached as `external`, so data is
preserved.

## Editing a live app afterward

- **compose.yaml** editor: apps imported from a VM/compose stack expose an **Edit compose**
  tab with a live YAML editor. Unsaved changes are flagged; **Save & redeploy** applies them.
- **values.yaml** editor: Kubernetes-native apps expose an **Edit values** tab connected over
  a live WebSocket; edits are saved straight to git (**Save to git**) and trigger a redeploy.
- **Environment variables**: app **Settings** → **Environment variables** tab. Each variable
  has a **Scope** (`build`, `runtime`, or `both`) and an optional **Secret** flag (encrypted at
  rest, masked as `••••••••` in the table, revealed on demand if you have permission).

## Gotchas

- The `Adopt`/`Split into applications` button ("Разбить на приложения" / "Split into
  applications") only appears on an app that is itself a still-combined compose stack; using
  it recreates the stack briefly (containers restart) but preserves volumes and data.
- Reveal-secret on env vars is permission-gated — if you get "You don't have permission to
  reveal secrets," ask an Owner/Admin.
- Editing `values.yaml`/`compose.yaml` directly bypasses the guided forms; a bad manifest can
  break the deploy. There's no diff/undo UI beyond git history itself.

## Not yet supported

- No in-console form to author a brand-new Compose stack from scratch (compose apps always
  originate from an adopted VM workload).
- No image-vulnerability scanning or registry credential management UI beyond the raw image
  string.
