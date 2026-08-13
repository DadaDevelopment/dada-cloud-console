# ddc — Dada Cloud CLI

**Console:** https://console.dada-tuda.ru

One command deploys a folder. No git remote, no Dockerfile, no YAML.

```bash
cd my-app
ddc deploy
```

First run opens a browser once (OAuth 2.0 device authorization grant, RFC 8628)
and caches the token in `~/.config/ddc/token.json` with mode 0600. Later runs
reuse it.

## What it does

1. Packs the directory into a `tar.gz`, honoring `.gitignore` and always
   skipping `.git`, `node_modules`, `.next`, `dist`, `build`, `venv`,
   `__pycache__`. Refuses upfront if the result would exceed the console's
   100MB limit.
2. Uploads it to the project/environment you pick (picked silently when you
   only have one of each).
3. Prints build status until the build finishes, then prints the live URL.

The stack is detected from the archive server-side — `package.json`,
`requirements.txt`, a `Dockerfile` if you have one, and so on.

## Commands

```
ddc login                    sign in via your browser
ddc deploy [dir] [--name X]  package and deploy dir (default: current directory)
```

`--name` sets the app name; without it the directory name is normalized into a
valid one.

## Install

One line, no Go and no clone needed:

```bash
curl -fsSL https://raw.githubusercontent.com/DadaDevelopment/dada-cloud-console/main/cli/install.sh | sh
```

Downloads the right `ddc` binary for your OS/arch from the [latest `cli-v*`
release](https://github.com/DadaDevelopment/dada-cloud-console/releases),
verifies it against the published `checksums.txt`, and installs it to
`~/.local/bin/ddc` (override with `DDC_INSTALL_DIR`).

If you already have a clone and Go 1.25+ installed, running `./install.sh`
locally builds from source instead — the release download is tried first and
falls back to `go build` automatically when no release binary is reachable.

## Environment overrides

| Variable | Default |
| --- | --- |
| `DDC_API_BASE` | `https://console.dada-tuda.ru/api/v1` |
| `DDC_ISSUER` | `https://id.dada-tuda.ru/realms/master` |
| `DDC_CLIENT_ID` | `ddc-cli` |
| `DDC_INSTALL_DIR` | `~/.local/bin` |
| `DDC_RELEASE_TAG` | latest `cli-v*` release |

## Attribution

Every request carries `X-Dada-Client: cli/<version>`, and, when an agent-session
environment variable is detected, `X-Dada-Agent-Session: <VAR_NAME>` — the name
of the variable found, never its value. The console records both as
`client_claimed` / `agent_session_claimed`: self-reported, never verified.
