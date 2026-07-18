# Deploy to Dada Cloud — GitHub Action

Deploy a **prebuilt** container image to a Dada Cloud app from your own CI. Your
workflow builds and pushes the image wherever it already does (GHCR, Docker Hub,
your registry); this action just tells Dada Cloud to roll the app to that image.

It does **not** build anything. It calls the platform deploy-hook endpoint
(`POST /api/v1/deploy`) with a revocable per-app token, which enqueues the same
`DeployImageVersion` rollout the console's "Deploy new image" button does.

## Get a token

Console → your project → the app → **Deploy from CI** → **Create token**. Copy
the `dadadh_…` token once (it is never shown again) and store it as a repo
secret named `DADA_DEPLOY_TOKEN`. Revoke it any time from the same panel.

## Usage

```yaml
- name: Deploy to Dada Cloud
  uses: dada-tuda/deploy-action@v1
  with:
    token: ${{ secrets.DADA_DEPLOY_TOKEN }}
    image: ghcr.io/${{ github.repository }}:${{ github.sha }}
```

Fail the job if the deploy does not land, and wait for it:

```yaml
- name: Deploy to Dada Cloud
  uses: dada-tuda/deploy-action@v1
  with:
    token: ${{ secrets.DADA_DEPLOY_TOKEN }}
    image: ghcr.io/${{ github.repository }}:${{ github.sha }}
    wait: 'true'
    timeout: '300'
```

### Inputs

| input      | required | default                          | description                                                            |
|------------|----------|----------------------------------|------------------------------------------------------------------------|
| `token`    | yes      | —                                | Deploy-hook token (`dadadh_…`). Pass from a secret, never inline.      |
| `image`    | yes      | —                                | Full image ref, e.g. `ghcr.io/acme/api:1.4.2` or `registry/x@sha256:…`. |
| `base-url` | no       | `https://console.dada-tuda.ru`   | Dada Cloud API base URL.                                                |
| `wait`     | no       | `false`                          | Poll until the deploy operation is terminal; fail the job if it failed.|
| `timeout`  | no       | `300`                            | Max seconds to wait when `wait: true`.                                  |

### Output

| output         | description                          |
|----------------|--------------------------------------|
| `operation-id` | Id of the enqueued deploy operation. |

`wait: true` treats an operation that reaches `Committed` (manifests written to
gitops) as success — that is the terminal state of a deploy operation. Live
rollout health after that is tracked in the console.

## No-action / plain curl form

The action is a thin wrapper. If you would rather not depend on it, one step
does the same thing:

```yaml
- name: Deploy to Dada Cloud
  run: |
    curl -fsS -X POST https://console.dada-tuda.ru/api/v1/deploy \
      -H "Authorization: Bearer $DADA_DEPLOY_TOKEN" \
      -H "Content-Type: application/json" \
      -d "{\"image\":\"ghcr.io/${GITHUB_REPOSITORY}:${GITHUB_SHA}\"}"
  env:
    DADA_DEPLOY_TOKEN: ${{ secrets.DADA_DEPLOY_TOKEN }}
```

## Monorepo / multiple apps

One token per app. In a monorepo that builds N images, create N tokens and add
one deploy step per app, each with its own `DADA_DEPLOY_TOKEN_<APP>` secret and
its own `image`.

## Publishing this action (maintainers)

The `uses: dada-tuda/deploy-action@v1` form needs this directory published as a
standalone **public** repo, because the source monorepo is private and Actions
cannot resolve `uses:` against it.

1. Create public repo `dada-tuda/deploy-action`.
2. Copy the contents of this directory (`action.yml`, `entrypoint.sh`, this
   README) to its root.
3. Tag `v1` (and move the `v1` tag on each release):
   ```
   git tag -f v1 && git push -f origin v1
   ```

Until that repo exists, users should use the **plain curl form** above — it
works today with zero dependencies.
