# DADA Cloud Console

GitOps-backed self-service cloud console for managing Kubernetes platform resources.

## One-click deploy button

Any public GitHub repository can carry a "Deploy on Dada" badge. A reader clicks it and the
repository is built and deployed into *their own* Dada Cloud account — no terminal, no fork, no
GitHub App install, and the reader does not need to own the repository.

Live example (deploys the starter template, not this monorepo):

[![Deploy on Dada](https://console.dada-tuda.ru/deploy-button.svg)](https://console.dada-tuda.ru/deploy?repo=DadaDevelopment/dadatuda-starter)

Add it to your own README:

```markdown
[![Deploy on Dada](https://console.dada-tuda.ru/deploy-button.svg)](https://console.dada-tuda.ru/deploy?repo=OWNER/REPO)
```

Optional query parameters: `branch` (default `main`) and `rootDir` (default `.`) for monorepos.
The console renders a ready-made snippet for you on any app that has a linked repository.
Full guide: [Add a "Deploy on Dada" button](frontend/content/docs/deploy-button-badge.md).

## Architecture

```
UI → Backend → DB → Worker Job → Git desired state → Argo CD → K8s CRD → Controllers → Status back to UI
```

## Quick Start

### Prerequisites

- Docker + Docker Compose
- Go 1.22+
- Node.js 20+

### 1. Initialize dev environment

```bash
make dev-init
```

This starts PostgreSQL, runs DB migrations (on first backend start), and initializes the local GitOps state repository at `/tmp/dada-state-repo`.

### 2. Start backend

```bash
make dev-backend
```

Backend runs at http://localhost:8080. On first start, it applies DB migrations and seeds dev data.

### 3. Start frontend

In a new terminal:

```bash
make dev-frontend
```

Frontend runs at http://localhost:3000.

### 4. Log in

Open http://localhost:3000 and log in with:

| Username | Password | Role |
|----------|----------|------|
| admin | admin | Platform Admin |
| alex | admin | Developer |
| client | admin | Client Admin |

### First Vertical Slice — ServiceDatabase

Try the full GitOps database creation flow:

1. Log in as `alex` (developer)
2. Select project **DADA Internal**
3. Go to **Databases**
4. Select environment `prod`
5. Click **Create Database**
6. Fill in: name=`codex-lb-db`, database=`codexlb`, app=`codex-lb`, backup enabled
7. Watch the **Operations** tab — status advances through: Created → Queued → Rendering → CommittingToGit → Committed → WaitingForArgoSync → Syncing → Reconciling → **Ready**
8. Check the GitOps repo: `ls /tmp/dada-state-repo/clusters/beget-prod/projects/internal/`

### Second Vertical Slice — Application Lifecycle

Try the full App deployment flow:

1. Log in as `alex` (developer)
2. Select project **DADA Internal**
3. Go to **Applications**
4. Select environment `prod`
5. Click **Create App**
6. Fill in: name=`my-service`, image=`ghcr.io/org/my-service:v1.0.0`, port=`8080`, replicas=`2`, profile=`small`
7. Watch the **Operations** tab — status advances through: Created → Queued → Rendering → CommittingToGit → Committed → WaitingForArgoSync → Syncing → Reconciling → **Ready**
8. Open the app card → click **Deploy Image** → enter a new image tag → watch the operation

## API

Backend API at http://localhost:8080/api/v1

### Auth

| Method | Path | Description |
|--------|------|-------------|
| POST | /api/v1/auth/login | Login |
| GET | /api/v1/auth/me | Current user |

### Projects & Environments

| Method | Path | Description |
|--------|------|-------------|
| GET | /api/v1/projects | List projects |
| GET | /api/v1/projects/:id | Project details (includes environments) |

### Databases

| Method | Path | Description |
|--------|------|-------------|
| GET | /api/v1/projects/:id/environments/:envId/databases | List databases |
| POST | /api/v1/projects/:id/environments/:envId/databases | Create database |

### Applications

| Method | Path | Description |
|--------|------|-------------|
| GET | /api/v1/projects/:id/environments/:envId/apps | List apps |
| POST | /api/v1/projects/:id/environments/:envId/apps | Create app |
| PATCH | /api/v1/projects/:id/environments/:envId/apps/:appName/image | Deploy new image |

### Operations

| Method | Path | Description |
|--------|------|-------------|
| GET | /api/v1/projects/:id/operations | List operations |
| GET | /api/v1/projects/:id/operations/:opId | Get operation |
| POST | /api/v1/projects/:id/operations/:opId/retry | Retry failed op |

## Operation State Machine

All mutations are async. The worker advances operations through these states:

```
Created → Queued → Rendering → CommittingToGit → Committed
  → WaitingForArgoSync → Syncing → Reconciling → Ready
                                                → Failed
```

## Typed Actions

| Action | Payload | Effect |
|--------|---------|--------|
| `CreateServiceDatabase` | name, database, app, backup | Renders `ServiceDatabase` CRD → GitOps commit |
| `CreateApp` | name, image, port, replicas, profile | Renders `App` CRD → GitOps commit |
| `DeployImageVersion` | app_name, image | Re-renders App manifest with new image → GitOps commit |

## GitOps Layout

```
clusters/beget-prod/
  projects/{slug}/
    environments/{env}/
      databases/{name}/db.yaml   # ServiceDatabase CRD
      apps/{name}/app.yaml       # App CRD
```

## Project structure

```
dada-cloud/
  backend/         # Go API + Worker
    cmd/server/    # Entry point
    internal/
      api/         # HTTP handlers + validation
      auth/        # JWT
      config/      # Config from env
      db/          # DB connection + migrations
      gitwriter/   # YAML renderer + Git commit
      models/      # Data models + operation payloads
      worker/      # Async operation processor
    migrations/    # SQL migrations
    tests/golden/  # Golden YAML files for renderer tests
  frontend/        # Next.js app
    app/           # App Router pages
    components/    # UI components
    lib/           # API client, auth, types
  helm/            # Helm chart (dada-cloud-console)
  docs/
    adr/           # Architecture Decision Records
    plans/         # Implementation plans
  scripts/         # Dev helper scripts
  docker-compose.yml
  Makefile
```

## Roadmap

- **v1** ✅ Foundation — ServiceDatabase creation, GitOps commits, status tracking
- **v2** 🚧 App lifecycle (CreateApp, DeployImageVersion), Gateway, Redis, RabbitMQ, restore
- **v3** Multi-tenant clients, quotas, plans, approval center
- **v4** Marketplace, object storage, AI workers, observability, cost tracking
