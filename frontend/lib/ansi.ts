import type { CSSProperties } from "react";

/**
 * A single run of text sharing one visual style, extracted from an
 * ANSI-encoded string. `style` is undefined for plain (unstyled) text so the
 * caller can skip wrapping it in a span.
 */
export interface AnsiSegment {
  text: string;
  style?: CSSProperties;
}

const ESC = String.fromCharCode(27);
const BEL = String.fromCharCode(7);

/**
 * Matches, in order: an SGR sequence (capturing its numeric params), any other
 * CSI sequence (cursor moves, erase — consumed and dropped), and an OSC
 * sequence. Built dynamically so the raw control bytes never appear in a static
 * regex literal.
 */
const ANSI_RE = new RegExp(
  ESC +
    "\\[([0-9;]*)m" +
    "|" +
    ESC +
    "\\[[0-9;?]*[ -/]*[@-~]" +
    "|" +
    ESC +
    "\\][^" +
    BEL +
    "]*" +
    BEL,
  "g"
);

/**
 * Terminal 16-colour palette tuned for a dark background: the standard names
 * mapped to Tailwind-adjacent hexes so output reads well on `bg-gray-900`.
 */
const BASIC = ["#4b5563", "#ef4444", "#22c55e", "#eab308", "#3b82f6", "#d946ef", "#06b6d4", "#d1d5db"];
const BRIGHT = ["#6b7280", "#f87171", "#4ade80", "#facc15", "#60a5fa", "#e879f9", "#22d3ee", "#f9fafb"];
const CUBE = [0, 95, 135, 175, 215, 255];

interface State {
  fg?: string;
  bg?: string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
  strike?: boolean;
}

/** Resolves an xterm 256-colour index to a CSS colour string. */
function xterm256(n: number): string {
  if (n < 8) return BASIC[n];
  if (n < 16) return BRIGHT[n - 8];
  if (n < 232) {
    const c = n - 16;
    return `rgb(${CUBE[Math.floor(c / 36) % 6]},${CUBE[Math.floor(c / 6) % 6]},${CUBE[c % 6]})`;
  }
  const v = 8 + (n - 232) * 10;
  return `rgb(${v},${v},${v})`;
}

/** Mutates `state` for one SGR parameter list (e.g. `[1, 38, 5, 196]`). */
function applySgr(state: State, params: number[]): void {
  for (let i = 0; i < params.length; i++) {
    const p = params[i];
    if (p === 0) {
      state.fg = state.bg = undefined;
      state.bold = state.dim = state.italic = state.underline = state.strike = false;
    } else if (p === 1) state.bold = true;
    else if (p === 2) state.dim = true;
    else if (p === 3) state.italic = true;
    else if (p === 4) state.underline = true;
    else if (p === 9) state.strike = true;
    else if (p === 22) state.bold = state.dim = false;
    else if (p === 23) state.italic = false;
    else if (p === 24) state.underline = false;
    else if (p === 29) state.strike = false;
    else if (p >= 30 && p <= 37) state.fg = BASIC[p - 30];
    else if (p === 39) state.fg = undefined;
    else if (p >= 40 && p <= 47) state.bg = BASIC[p - 40];
    else if (p === 49) state.bg = undefined;
    else if (p >= 90 && p <= 97) state.fg = BRIGHT[p - 90];
    else if (p >= 100 && p <= 107) state.bg = BRIGHT[p - 100];
    else if (p === 38 || p === 48) {
      const mode = params[i + 1];
      let col: string | undefined;
      if (mode === 5) {
        col = xterm256(params[i + 2] ?? 0);
        i += 2;
      } else if (mode === 2) {
        col = `rgb(${params[i + 2] ?? 0},${params[i + 3] ?? 0},${params[i + 4] ?? 0})`;
        i += 4;
      }
      if (col) {
        if (p === 38) state.fg = col;
        else state.bg = col;
      }
    }
  }
}

/** Snapshots the current `state` into a React style object, or undefined if plain. */
function styleOf(state: State): CSSProperties | undefined {
  const s: CSSProperties = {};
  if (state.fg) s.color = state.fg;
  if (state.bg) s.backgroundColor = state.bg;
  if (state.bold) s.fontWeight = 600;
  if (state.dim) s.opacity = 0.6;
  if (state.italic) s.fontStyle = "italic";
  const deco = [state.underline ? "underline" : "", state.strike ? "line-through" : ""].filter(Boolean).join(" ");
  if (deco) s.textDecoration = deco;
  return Object.keys(s).length ? s : undefined;
}

/**
 * Parses a string containing ANSI SGR escape codes into styled segments.
 * Non-SGR escape sequences are stripped. Strings with no escape bytes return a
 * single plain segment, so plain logs incur no styling overhead.
 */
export function parseAnsi(input: string): AnsiSegment[] {
  if (!input) return [];
  if (input.indexOf(ESC) === -1) return [{ text: input }];

  const segments: AnsiSegment[] = [];
  const state: State = {};
  let last = 0;
  ANSI_RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = ANSI_RE.exec(input)) !== null) {
    if (m.index > last) segments.push({ text: input.slice(last, m.index), style: styleOf(state) });
    last = ANSI_RE.lastIndex;
    if (m[1] !== undefined) {
      const params = m[1] === "" ? [0] : m[1].split(";").map((x) => parseInt(x, 10) || 0);
      applySgr(state, params);
    }
  }
  if (last < input.length) segments.push({ text: input.slice(last), style: styleOf(state) });
  return segments;
}
