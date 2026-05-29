# Design: values.yaml Live Editor

## Understanding Summary

- **What**: WebSocket-based live editor for `values.yaml` per app, accessible from the app detail page
- **Why**: Dev/admin users need to tweak Helm values without touching git directly; gitops-agent is the single git-gateway
- **Who**: Users with `canWrite()` — `developer` + `platform-admin`
- **Key constraint**: Console backend MUST NOT have git credentials; only gitops-agent touches git
- **Non-goals**: conflict resolution for parallel editors, YAML schema validation, diff view, streaming collaborative editing (Y.js)

---

## Architecture

```
frontend
  │  1. POST /api/v1/projects/:projectId/environments/:envId/apps/:appName/values-token
  │     console backend: checks canWrite(), signs HMAC token (TTL 60s), returns {token, ws_url}
  │
  │  2. WS  ws://<gitops-agent>/ws/values?token=...&project=...&env=...&app=...
  └──────────────────────────────────────────────────────▶ gitops-agent
```

### WebSocket Protocol

| Direction        | Message                                   |
|-----------------|-------------------------------------------|
| agent → client  | `{type:"content", yaml:"..."}`  on connect |
| agent → client  | `{type:"update",  yaml:"..."}`  on git pull |
| client → agent  | `{type:"save",    yaml:"..."}`             |
| agent → client  | `{type:"committed", sha:"..."}`            |
| agent → client  | `{type:"error",   message:"..."}`          |

---

## Components

### 1. gitops-agent

**`internal/wstoken/token.go`**
```go
type Claims struct { Project, Env, App string; Exp int64 }
func Sign(secret string, c Claims) (string, error)
func Verify(secret, token string) (Claims, error)
// encoding: json(claims) + "." + base64(hmac-sha256)
// Verify checks signature AND Exp > time.Now().Unix()
```

**`internal/server/hub.go`**
```go
type Hub struct {
    mu       sync.RWMutex
    sessions map[string][]*Session  // key = "project/env/app"
}
func (h *Hub) Register(s *Session)
func (h *Hub) Unregister(s *Session)
func (h *Hub) Notify(project, env, app, yaml string)
```

**`internal/server/ws_handler.go`** — `/ws/values`
1. Verify token → extract project, env, app
2. `mgr.ReadFile(renderer.AppHelmValuesGitPath(project, env, app))`
3. Send `{type:"content", yaml}`
4. `hub.Register(session)`
5. Read loop: on `{type:"save"}` → `yaml.Unmarshal` (syntax check) → `mgr.CommitAndPush` → `db.InsertCommit` → send `{type:"committed", sha}`
6. Defer `hub.Unregister`

**`Server` struct** gets new deps: `pool *pgxpool.Pool`, `mgr *git.Manager`, `hub *Hub`, `tokenSecret string`

**`internal/worker/gitwatcher.go`** — after processing each commit:
```go
for _, f := range commit.Files {
    if strings.HasSuffix(f, "/values.yaml") {
        project, env, app := parseValuesPath(f)
        content, _ := mgr.ReadFile(f)
        hub.Notify(project, env, app, content)
    }
}
```

---

### 2. console backend

**`internal/api/apps_values.go`**
```
POST /api/v1/projects/:projectId/environments/:envId/apps/:appName/values-token
→ canWrite() check
→ resolve project.slug, env.slug from DB
→ wstoken.Sign(secret, Claims{Project, Env, App, Exp: now+60s})
→ 200 { token, ws_url }
```

**`internal/config/config.go`** — two new fields:
```go
GitopsAgentTokenSecret string  // GITOPS_AGENT_TOKEN_SECRET
GitopsAgentWSURL       string  // GITOPS_AGENT_WS_URL
```

**`wstoken` package** — duplicated (~20 lines) from gitops-agent into backend, or extracted to shared internal package. Monorepo: duplicate is acceptable here.

---

### 3. frontend

**New packages:**
```
@codemirror/view @codemirror/state @codemirror/lang-yaml codemirror
```

**`components/ui/yaml-editor.tsx`**
- Controlled CodeMirror with `yaml()` extension + `oneDark` theme
- Props: `value`, `onChange`, `readOnly?`
- Min height 400px

**`app/(console)/projects/[projectId]/apps/[appName]/values/page.tsx`**
- Fetch token → open WS
- Handle messages: content/update → setYaml; committed → toast + setDirty(false); error → toast.error
- Save: `ws.send({type:"save", yaml})`, disabled while saving or not dirty
- `Ctrl/Cmd+S` shortcut
- WS status indicator (green/yellow/red dot)
- On unmount: `ws.close()`
- If update arrives while dirty → toast "File updated in git" (no auto-overwrite)
- If WS drops → show "Reconnect" button (no silent retry)

**`lib/api.ts`** — add:
```ts
valuesApi.getToken(projectId, envId, appName)
  → POST .../values-token → { token: string, ws_url: string }
```

---

## Decision Log

| Decision | Alternatives considered | Reason |
|---|---|---|
| gitops-agent owns WS | console backend proxies | Single git-gateway; no git creds in backend |
| WebSocket over SSE+REST | SSE+REST, polling | Bidirectional: live updates + save in one connection |
| Delegate token (HMAC TTL 60s) | shared secret in header, frontend JWT to agent | Clean: backend controls identity, agent validates cheaply |
| CodeMirror over Monaco | Monaco, react-ace | Lighter, no Electron deps, tree-shakeable |
| Duplicate wstoken (~20 lines) | shared Go module | Monorepo simplicity; code is trivial |
| Direct git commit (no operations table) | UpdateAppValues operation | Read and write go through same gateway; no async needed for file edit |
| Log to git_commits | skip audit | Audit trail without operations overhead |

---

## Assumptions

- `GITOPS_AGENT_TOKEN_SECRET` + `GITOPS_AGENT_WS_URL` added to both services' env
- gitops-agent publicly reachable (Ingress or dedicated NodePort) for WS
- File not found → agent sends template with zero values (image:"", port:8080, replicas:1, profile:"small")
- One editor session per app at a time is acceptable (no locking needed for v1)
