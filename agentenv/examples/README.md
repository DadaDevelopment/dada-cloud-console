# Editor and agent configuration

`agentenv mcp` is a local stdio MCP server. Claude Code, Cursor and Codex see `env_create`,
`env_attach` and `env_promote` as ordinary tools — **with no account anywhere and no network call
to any vendor**, because the `local` adapter runs against your own machine.

That is the point of the packaging. The profile is worth reading only if the client is worth
running before anyone else has implemented anything.

- `claude-code/.mcp.json` — drop into a project root.
- Cursor and Codex take the same stdio server; consult their current config location.

## AGENTS.md snippet

Paste into a project's `AGENTS.md` or `CLAUDE.md` so the agent knows a body is available and,
more importantly, knows to read the contract before acting:

```md
## Remote environments

An `agentenv` MCP server is configured. When a task needs a throwaway machine — a long build, a
risky install, several tasks in parallel — call `env_create` instead of working in this checkout.

Call `env_get` first and read `capabilities` and `guarantees`. They tell you what is permitted
here and what survives promotion, so you do not discover a limit by hitting it.

Do not call `env_promote` without being asked: it makes the environment permanent and starts
charging.
```
