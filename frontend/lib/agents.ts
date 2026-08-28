/**
 * Reading one agent back out of the snapshot the console stored for it.
 *
 * A save re-states every field, so the editor has to be able to re-fill itself
 * from the stored summary exactly: a tool list this mapper drops does not open
 * as an empty checkbox, it is deleted from git by the next save of an unrelated
 * field. That is why the mapping lives here with tests rather than inline in
 * the page.
 */

import type { AgentEnvVar, AgentToolHeader, AgentToolRef, ResourceSnapshot } from "./types";

export interface AgentFormValues {
  name: string;
  display_name: string;
  description: string;
  prompt: string;
  prompt_version: string;
  model_config: string;
  tools: string[];
  customTools: CustomToolForm[];
  env: string;
}

/**
 * An MCP server this agent brings itself, as the editor holds it.
 *
 * Headers are one "Name: value" per line for the same reason env is: the value
 * of an Authorization header is one long opaque string, and a row of inputs per
 * header turns three headers into a wall of boxes. A value may refer to the
 * agent's own env as ${VAR}, which is what keeps a token out of this field.
 */
export interface CustomToolForm {
  name: string;
  url: string;
  protocol: string;
  description: string;
  headers: string;
}

export const EMPTY_CUSTOM_TOOL: CustomToolForm = {
  name: "",
  url: "",
  protocol: "STREAMABLE_HTTP",
  description: "",
  headers: "",
};

export const EMPTY_AGENT_FORM: AgentFormValues = {
  name: "",
  display_name: "",
  description: "",
  prompt: "",
  prompt_version: "",
  model_config: "",
  tools: [],
  customTools: [],
  env: "",
};

/**
 * Reads the KEY=value lines of the env textarea.
 *
 * A line with no "=" is a name with an empty value rather than a parse error,
 * so a half-typed line does not throw the rest of the form away. Only the first
 * "=" splits, because values carry them (DSNs, base64, flags).
 */
export function parseEnvLines(text: string): AgentEnvVar[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const at = line.indexOf("=");
      if (at < 0) return { name: line, value: "" };
      return { name: line.slice(0, at).trim(), value: line.slice(at + 1) };
    })
    .filter((e) => e.name !== "");
}

/** Renders stored env entries back into the textarea's one-per-line form. */
export function envLines(summary: Record<string, unknown>): string {
  const env = summary.env;
  if (!Array.isArray(env)) return "";
  return env
    .map((e) => {
      const entry = e as { name?: unknown; value?: unknown };
      return entry?.name ? `${String(entry.name)}=${String(entry.value ?? "")}` : "";
    })
    .filter(Boolean)
    .join("\n");
}

/**
 * Reads the "Name: value" lines of a header textarea.
 *
 * Only the first ":" splits: header values carry them (a URL, a JWT). A line
 * without one is a header with an empty value rather than a parse error, so a
 * half-typed line does not throw the rest of the form away.
 */
export function parseHeaderLines(text: string): AgentToolHeader[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const at = line.indexOf(":");
      if (at < 0) return { name: line, value: "" };
      return { name: line.slice(0, at).trim(), value: line.slice(at + 1).trim() };
    })
    .filter((h) => h.name !== "");
}

/** Renders stored headers back into the textarea's one-per-line form. */
export function headerLines(headers: AgentToolHeader[] | undefined): string {
  if (!Array.isArray(headers)) return "";
  return headers
    .filter((h) => h?.name)
    .map((h) => `${h.name}: ${h.value ?? ""}`)
    .join("\n");
}

/**
 * The MCP servers this agent owns, split out of the stored tool list.
 *
 * A tool with an address is one the claim declares itself; a tool without one
 * points at a server that already exists in the runtime. They are two different
 * controls in the editor -- a form and a checkbox -- and mixing them would let
 * a checkbox save erase the address of a server the user typed.
 */
export function customTools(summary: Record<string, unknown>): CustomToolForm[] {
  const tools = summary.tools;
  if (!Array.isArray(tools)) return [];
  return tools
    .map((ref) => ref as AgentToolRef)
    .filter((ref) => ref?.name && ref?.url)
    .map((ref) => ({
      name: String(ref.name),
      url: String(ref.url ?? ""),
      protocol: String(ref.protocol ?? "STREAMABLE_HTTP"),
      description: String(ref.description ?? ""),
      headers: headerLines(ref.headers),
    }));
}

/** Turns one editor row back into the tool reference the API takes. */
export function customToolToRef(tool: CustomToolForm): AgentToolRef {
  const headers = parseHeaderLines(tool.headers);
  return {
    name: tool.name.trim(),
    url: tool.url.trim(),
    protocol: tool.protocol || undefined,
    description: tool.description.trim() || undefined,
    headers: headers.length > 0 ? headers : undefined,
  };
}

/** Names of the shared MCP servers the stored agent references. */
export function toolNames(summary: Record<string, unknown>): string[] {
  const tools = summary.tools;
  if (!Array.isArray(tools)) return [];
  return tools
    .map((ref) => ref as AgentToolRef)
    .filter((ref) => ref?.name && !ref?.url)
    .map((ref) => String(ref.name));
}

/** Fills the editor from what the console stored for this agent. */
export function agentFormFromSnapshot(agent: ResourceSnapshot): AgentFormValues {
  const summary = (agent.summary_json ?? {}) as Record<string, unknown>;
  return {
    name: agent.name,
    display_name: String(summary.display_name ?? ""),
    description: String(summary.description ?? ""),
    prompt: String(summary.prompt ?? ""),
    prompt_version: String(summary.prompt_version ?? ""),
    model_config: String(summary.model_config ?? ""),
    tools: toolNames(summary),
    customTools: customTools(summary),
    env: envLines(summary),
  };
}

/**
 * The kind the console writes for an agent it owns.
 *
 * The list is a view of git, and git holds two kinds under the same idea: the
 * ManagedAgent claim the console renders, and the raw kagent Agent CR that a
 * hand-maintained resources.values.yaml carries. Both arrive through the same
 * git reader, so both belong on the page; only the claim has a console git path
 * behind it.
 */
export const CONSOLE_AGENT_KIND = "ManagedAgent";

/**
 * Reports whether this agent can be edited or deleted here.
 *
 * A hand-written Agent is read-only in the console on purpose: a claim named
 * after it would compose a SECOND CR with that name into the runtime namespace,
 * and the two owners would fight over it. The backend refuses such a save with
 * 409; hiding the buttons is what keeps the user from meeting that refusal.
 */
export function isConsoleOwnedAgent(kind: string): boolean {
  return kind === CONSOLE_AGENT_KIND;
}
