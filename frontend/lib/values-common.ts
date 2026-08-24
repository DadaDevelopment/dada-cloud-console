/**
 * Minimal, dependency-free reader/patcher for the `common.*` block of an app's
 * Helm values.yaml. The block is template-generated (canonical 2-space indent),
 * so a path-scoped line editor is reliable and preserves the rest of the file
 * (comments, unknown keys, formatting) byte-for-byte. This is intentionally not
 * a general YAML parser — it only touches the fixed set of common.* fields the
 * structured editor exposes; the raw YAML editor remains the escape hatch.
 */

export interface CommonConfig {
  imageName: string;
  imageTag: string;
  servicePort: string;
  replicas: string;
  useDotEnv: string;
  reqCpu: string;
  reqMemory: string;
  limCpu: string;
  limMemory: string;
}

const PATHS: Record<keyof CommonConfig, string> = {
  imageName: "common.image.name",
  imageTag: "common.image.tag",
  servicePort: "common.servicePort",
  replicas: "common.replicas",
  useDotEnv: "common.useDotEnv",
  reqCpu: "common.resources.requests.cpu",
  reqMemory: "common.resources.requests.memory",
  limCpu: "common.resources.limits.cpu",
  limMemory: "common.resources.limits.memory",
};

interface LineInfo {
  idx: number;
  indent: number;
  key: string;
  value: string;
}

function scan(yaml: string): Map<string, LineInfo> {
  const map = new Map<string, LineInfo>();
  const stack: { indent: number; key: string }[] = [];
  yaml.split("\n").forEach((raw, idx) => {
    const m = raw.match(/^(\s*)([A-Za-z0-9_.-]+):(.*)$/);
    if (!m) return;
    const indent = m[1].length;
    const key = m[2];
    while (stack.length && stack[stack.length - 1].indent >= indent) stack.pop();
    const path = [...stack.map((s) => s.key), key].join(".");
    map.set(path, { idx, indent, key, value: m[3].trim() });
    stack.push({ indent, key });
  });
  return map;
}

function unquote(v: string): string {
  if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) {
    return v.slice(1, -1);
  }
  return v;
}

/** readCommon extracts the current common.* values; missing keys come back "". */
export function readCommon(yaml: string): CommonConfig {
  const map = scan(yaml);
  const get = (path: string) => {
    const value = unquote(map.get(path)?.value ?? "");
    return value === "~" || value === "null" ? "" : value;
  };
  return {
    imageName: get(PATHS.imageName),
    imageTag: get(PATHS.imageTag),
    servicePort: get(PATHS.servicePort),
    replicas: get(PATHS.replicas),
    useDotEnv: get(PATHS.useDotEnv) || "false",
    reqCpu: get(PATHS.reqCpu),
    reqMemory: get(PATHS.reqMemory),
    limCpu: get(PATHS.limCpu),
    limMemory: get(PATHS.limMemory),
  };
}

function formatValue(field: keyof CommonConfig, value: string): string {
  if (field === "useDotEnv") return `"${value}"`;
  if (field === "servicePort" && !value.trim()) return "~";
  return value;
}

/**
 * directCommonFields are the common.* fields that live as a direct child of
 * `common`, so a file missing one can have it inserted back. The nested fields
 * (image.*, resources.*) are always emitted by the template.
 */
const directCommonFields: (keyof CommonConfig)[] = ["servicePort", "replicas", "useDotEnv"];

/**
 * insertUnderCommon adds `key: value` as the first child of the top-level
 * `common:` mapping, matching the indent the file already uses for its other
 * children. A file with no `common:` block is left untouched.
 */
function insertUnderCommon(lines: string[], key: string, value: string): string[] {
  const commonIdx = lines.findIndex((l) => /^common:\s*$/.test(l));
  if (commonIdx < 0) return lines;
  let indent = 2;
  for (let i = commonIdx + 1; i < lines.length; i++) {
    const m = lines[i].match(/^(\s+)[A-Za-z0-9_.-]+:/);
    if (m) {
      indent = m[1].length;
      break;
    }
  }
  const out = lines.slice();
  out.splice(commonIdx + 1, 0, `${" ".repeat(indent)}${key}: ${value}`);
  return out;
}

/**
 * patchCommon replaces the value of each known common.* line in place, preserving
 * indentation, key, and the rest of the document.
 *
 * A direct child of `common` that the file does not carry is INSERTED rather
 * than skipped. Skipping it silently dropped the field while the save still
 * reported success: a worker app renders with no `common.servicePort` line at
 * all (renderer emits it omitempty and the app's port is 0), so typing a port
 * into this form committed a values.yaml without it and the field came back
 * empty on the next read -- the port "reverting" on save. Nested fields
 * (image.*, resources.*) are still skipped when absent; the template always
 * emits them.
 */
export function patchCommon(yaml: string, cfg: CommonConfig): string {
  let lines = yaml.split("\n");
  const map = scan(yaml);
  (Object.keys(PATHS) as (keyof CommonConfig)[]).forEach((field) => {
    const info = map.get(PATHS[field]);
    if (!info) return;
    lines[info.idx] = `${" ".repeat(info.indent)}${info.key}: ${formatValue(field, cfg[field])}`;
  });

  for (const field of directCommonFields) {
    if (map.get(PATHS[field]) || !cfg[field].trim()) continue;
    const key = PATHS[field].slice("common.".length);
    lines = insertUnderCommon(lines, key, formatValue(field, cfg[field]));
  }

  // Keep the no-route choice explicit for Helm. Older values files do not
  // carry common.service.enabled, so add it next to servicePort when needed.
  const enabled = cfg.servicePort.trim() !== "";
  const after = scan(lines.join("\n"));
  const enabledInfo = after.get("common.service.enabled");
  const portInfo = after.get(PATHS.servicePort);
  if (enabledInfo) {
    lines[enabledInfo.idx] = `${" ".repeat(enabledInfo.indent)}${enabledInfo.key}: ${enabled}`;
  } else if (portInfo) {
    lines.splice(portInfo.idx + 1, 0, `${" ".repeat(portInfo.indent)}service:`, `${" ".repeat(portInfo.indent + 2)}enabled: ${enabled}`);
  }
  return lines.join("\n");
}
