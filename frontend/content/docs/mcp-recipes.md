# MCP recipes: worked flows

## What it's for

The exact tool sequences behind the things people actually ask an agent to do on
DADA Cloud — deploy an image, wire up a repository, get a sandbox with a database
in it, chase down a failing app. Each recipe gives you a sentence you can say to
your assistant and the calls it should make, so you can tell whether the agent is
doing the right thing or improvising.

Setup first: [Control DADA Cloud from an AI agent (MCP)](mcp-ai-agents.md).
Full argument lists: [MCP tool reference](mcp-tool-reference.md).

## The two calls that start everything

Almost every recipe below opens the same way, because `projectId` and `envId` are
UUIDs and nothing else hands them out:

```
listProjects                    → pick a project, take its id
getProject(projectId)           → take the environment's id
```

An agent that skips this and passes the project slug it saw in a URL gets a 404
on every subsequent call. If yours does that, tell it: *"project ids are UUIDs,
call listProjects first."*

## Recipe: deploy a container image

> "Deploy `ghcr.io/acme/api:1.4.0` into my project as `api` on port 8080, with
> `LOG_LEVEL=info` and a secret `DATABASE_URL`."

```
listProjects
getProject(projectId)
createApp(projectId, envId, name="api",
          image="ghcr.io/acme/api:1.4.0", port=8080)   → operation id
getOperation(projectId, operationId)                   → poll to Committed
setEnvVar(projectId, envId, appName="api",
          key="LOG_LEVEL", value="info")
setEnvVar(projectId, envId, appName="api",
          key="DATABASE_URL", value="…", is_secret=true)
listApps(projectId, envId)                             → poll until phase Healthy
```

Two things go wrong here in practice. **Committed is not Healthy** — an agent
that stops at the operation will tell you the deploy succeeded when the container
is crash-looping. And **there is no restart tool**, because there is nothing to
restart: variable and image changes reconcile on their own.

If the phase never turns Healthy, go to the diagnose recipe.

## Recipe: ship a new version of something already running

> "Roll `api` to `1.4.1`."

```
listProjects
getProject(projectId)
updateAppImage(projectId, envId, appName="api",
               image="ghcr.io/acme/api:1.4.1")         → operation id
getOperation(projectId, operationId)                   → poll to Committed
listApps(projectId, envId)                             → poll until phase Healthy
```

## Recipe: deploy from a GitHub repository

> "Connect `acme/api` and build it."

```
listProjects
getProject(projectId)
listGitInstallations(projectId)
```

If the list is empty, the agent **cannot** fix that for you — installing a GitHub
App is a human action on GitHub's own consent screen. It should hand you a link:

```
getGitInstallUrl(projectId, provider="github")   → open this yourself, then continue
```

With an installation in place:

```
listInstallationRepos(projectId, installationId)
connectGitRepo(projectId, envId, installation_id=…,
               repo_full_name="acme/api",
               production_branch="main", auto_deploy=true,
               port=8080)
triggerBuild(projectId, envId, appName="api")    → returns a build, not an operation
getBuild(projectId, buildId)                     → poll this one
listApps(projectId, envId)                       → poll until phase Healthy
```

Note the asymmetry: `triggerBuild` is imperative and gives you a **build id**, so
`getOperation` is the wrong tool to watch it with. Everything else in this
document is watched with `getOperation`.

More background: [Deploy an app from GitHub](applications-deploy-from-github.md).

## Recipe: add a managed Postgres

> "Give `api` a database and point it at it."

```
listProjects
getProject(projectId)
createDatabase(projectId, envId, name="api-db", app_ref="api",
               backup_enabled=true)              → operation id
getOperation(projectId, operationId)             → poll to Committed
listDatabases(projectId, envId)                  → poll until the phase is ready
getDatabaseCredentials(projectId, envId,
                       name="api-db", reveal=true)
setEnvVar(projectId, envId, appName="api",
          key="DATABASE_URL", value="…", is_secret=true)
```

`getDatabaseCredentials` returns **404 while the database is still
provisioning** — that is "not yet", not "not found". An agent that treats the
404 as fatal will give up one poll too early; tell it to keep polling
`listDatabases` first.

Every reveal is audited, so expect these calls to show up in your audit trail
with the agent's — which is to say your — identity on them.

More background: [Managed Postgres databases](databases-postgres.md).

## Recipe: get a sandbox to work in

This is the flow an agent should reach for when it needs somewhere to *run*
things rather than somewhere to deploy them.

> "Get me a box with a Postgres and expose port 3000."

```
getBoxCatalog                                    → real image and profile names
listProjects
boxUp(projectId, name="scratch", image=…, profile=…,
      ttl_seconds=…, spend_cap_rub=…,
      ssh_public_key="ssh-ed25519 AAAA…")
```

`boxUp` is synchronous and returns only once a command has actually run inside
the box. Its response carries the connection coordinates, a one-time `dadabox_`
token, and a paste-ready `mcpServers` snippet.

**Add that snippet to your client as a second MCP server.** That is the whole
design: the box's own endpoint is what runs your commands, so your code and your
model credentials never travel through our control plane. There is no tool on
this server that runs a command, and there will not be one.

Then:

```
attachBoxDatabase(projectId, boxName="scratch", name="db")
listBoxAttachments(projectId, boxName="scratch")   → which env KEYS were injected
exposeBox(projectId, boxName="scratch", port=3000) → platform-assigned hostname
```

The database is provisioned *outside* the box and injected into its env file, so
deleting the box never destroys your data. You do not get to choose the exposed
hostname — that is a crystallization feature, not an ephemeral-sandbox one.

Housekeeping the agent should do without being asked:

```
getBoxUsage(projectId, boxName="scratch")   → what this has cost so far
suspendBox(projectId, boxName="scratch")    → stop compute billing, keep the disk
resumeBox(projectId, boxName="scratch")     → same box, same disk, same credentials
deleteBox(projectId, boxName="scratch")     → destructive; releases the capacity
```

`ttl_seconds` puts the box to **sleep**, it never destroys it. A reached
`spend_cap_rub` suspends it, for the same reason: a runaway should cost you money
and never your data.

## Recipe: promote a sandbox into a real VM

> "This works — make it permanent on `api.acme.com`."

```
crystallizeBox(projectId, boxName="scratch",
               app_server_name="api-prod",
               domain="api.acme.com", probe_path="/healthz",
               ack_monthly_charge=true)
listBoxCrystallizations(projectId, boxName="scratch")   → the verification report
listAppServers(projectId)
```

`ack_monthly_charge` is consent, not a flag: without it the call returns 409.
Promotion turns a per-minute body into a monthly VM bill, so an agent cannot
commit you to that by accident.

Read `verified` in the report separately from `status`. An attempt can finish and
still fail verification — file manifests, the listening-socket set, a per-key env
hash comparison and an end-to-end HTTP probe all have to agree.

## Recipe: diagnose a failing app

> "`api` is unhealthy, what's wrong?"

```
listProjects
getProject(projectId)
listApps(projectId, envId)                        → read the phase
searchLogs(projectId, app="api", since="1h", size=200)
searchLogs(projectId, app="api", q="error", since="6h")
getBuild(projectId, buildId)                      → if it looks build-related
```

For a VM/Compose app, pass `vm="<server name>"` instead of `app=`. One of the two
is required — that scoping check is what keeps one tenant out of another's logs.

The built-in `diagnose-app` prompt does exactly this sequence and is told not to
mutate anything, which makes it the safe thing to hand an agent that is about to
start guessing.

More background:
[Monitoring: metrics, logs, alerts](monitoring-metrics-logs-alerts.md).

## Recipe: attach a custom domain

```
addDomainAuthorization(projectId, apex_domain="acme.com")  → TXT challenge
# publish the TXT record at your DNS provider yourself
verifyDomainAuthorization(projectId, id=…)
```

Authorizing the apex covers its subdomains. **Binding a verified hostname to a
specific app is not on the agent surface** — finish that step in the console.

More background: [Custom domains and HTTPS](domains-and-https.md).

## When an agent gets stuck

Four failure modes cover nearly everything:

1. **It passed a slug where a UUID was wanted.** Symptom: 404 on every call.
   Fix: `listProjects`, then `getProject`.
2. **It stopped at Committed.** Symptom: "deployed successfully" for an app that
   is crash-looping. Fix: make it poll `listApps` for the phase.
3. **It is looking for a tool that does not exist.** Symptom: it proposes
   restarting, deleting, or running a shell command. Those are absent on purpose;
   point it at the [tool reference](mcp-tool-reference.md).
4. **It gave up on a 404 that meant "not yet".** Symptom: abandoning a database
   right after creating it. Fix: poll the list tool until the phase is ready.
