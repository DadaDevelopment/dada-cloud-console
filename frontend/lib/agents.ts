/**
 * Reading one agent back out of the snapshot the console stored for it.
 *
 * A save re-states every field, so the editor has to be able to re-fill itself
 * from the stored summary exactly: a tool list this mapper drops does not open
 * as an empty checkbox, it is deleted from git by the next save of an unrelated
 * field. That is why the mapping lives here with tests rather than inline in
 * the page.
 */

import type { AgentEnvVar, ResourceSnapshot } from "./types";

export interface AgentFormValues {
  name: string;
  display_name: string;
  description: string;
  prompt: string;
  prompt_version: string;
  model_config: string;
  tools: string[];
  env: string;
}

export const EMPTY_AGENT_FORM: AgentFormValues = {
  name: "",
  display_name: "",
  description: "",
  prompt: "",
  prompt_version: "",
  model_config: "",
  tools: [],
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

/** Names of the MCP servers the stored agent references. */
export function toolNames(summary: Record<string, unknown>): string[] {
  const tools = summary.tools;
  if (!Array.isArray(tools)) return [];
  return tools
    .map((ref) => {
      const entry = ref as { name?: unknown };
      return entry?.name ? String(entry.name) : "";
    })
    .filter(Boolean);
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
