/**
 * Parser for pasted environment blobs.
 *
 * The console used to accept variables one at a time through a modal with four
 * inputs. For the flow this product leads with — upload a folder, run a bot —
 * the user already has a `.env` file in front of them, and retyping it key by
 * key is the single most tedious step of onboarding. This turns that file, or a
 * one-line `A=1, B=2` paste, into rows.
 *
 * Deliberately forgiving about what people actually paste: `export` prefixes
 * from a shell profile, `#` comments, blank lines, quoted values, and CRLF.
 * Deliberately strict about keys, because an invalid key is silently useless at
 * runtime and the user should hear about it while the paste is still on screen.
 */

export interface ParsedEnvVar {
  key: string;
  value: string;
}

export interface ParsedEnvResult {
  vars: ParsedEnvVar[];
  errors: string[];
}

const KEY_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

/**
 * Splits one physical line into logical assignments.
 *
 * A `.env` file has one assignment per line, but people also paste
 * `A=1, B=2` — so a comma is treated as a separator only when what follows it
 * looks like the start of another assignment. That keeps commas inside values
 * (`ALLOWED_HOSTS=a.ru,b.ru`) intact, which is the far more common case.
 * A quoted value is never split.
 */
function splitAssignments(line: string): string[] {
  if (/^\s*[A-Za-z_][A-Za-z0-9_]*\s*=\s*["']/.test(line)) return [line];

  const parts: string[] = [];
  let start = 0;
  const re = /,\s*(?=(?:export\s+)?[A-Za-z_][A-Za-z0-9_]*\s*=)/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(line)) !== null) {
    parts.push(line.slice(start, m.index));
    start = m.index + m[0].length;
  }
  parts.push(line.slice(start));
  return parts;
}

function unquote(raw: string): string {
  const v = raw.trim();
  if (v.length >= 2 && ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'")))) {
    const inner = v.slice(1, -1);
    return v.startsWith('"') ? inner.replace(/\\n/g, "\n").replace(/\\"/g, '"') : inner;
  }
  return v;
}

/** Parses a pasted `.env` blob into variables, collecting per-line problems. */
export function parseEnvBlob(input: string): ParsedEnvResult {
  const vars: ParsedEnvVar[] = [];
  const errors: string[] = [];
  const seen = new Set<string>();

  for (const rawLine of input.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) continue;

    for (const rawPart of splitAssignments(line)) {
      const part = rawPart.trim();
      if (!part) continue;

      const eq = part.indexOf("=");
      if (eq === -1) {
        errors.push(part);
        continue;
      }

      const key = part.slice(0, eq).trim().replace(/^export\s+/, "");
      if (!KEY_RE.test(key)) {
        errors.push(part);
        continue;
      }

      const value = unquote(part.slice(eq + 1));
      if (seen.has(key)) {
        vars[vars.findIndex((v) => v.key === key)] = { key, value };
      } else {
        seen.add(key);
        vars.push({ key, value });
      }
    }
  }

  return { vars, errors };
}
