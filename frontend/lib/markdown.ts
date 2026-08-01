/**
 * Dependency-free Markdown-to-HTML renderer, scoped to the syntax used by the
 * dada-cloud user guides in `content/docs/*.md`. It is intentionally NOT a full
 * CommonMark implementation: it supports headings (`#`..`######`), paragraphs,
 * ordered/unordered lists with one level of nesting and wrapped continuation
 * lines, fenced ```code``` blocks, `inline code`, **bold**, *italic*,
 * `[text](url)` links, blockquotes, horizontal rules and pipe tables. Every
 * piece of text is HTML-escaped before formatting markers are applied, so the
 * output is safe to inject with `dangerouslySetInnerHTML`.
 *
 * Internal links that point at another guide (`something.md`) are rewritten to a
 * document-relative slug (`something`) so navigation stays inside the current
 * locale subtree without the renderer needing to know the active locale.
 */

const LIST_ITEM = /^(\s*)([-*+]|\d+\.)(\s+)(.*)$/;
const CODE_OPEN = "\uE000";
const CODE_CLOSE = "\uE001";

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function indentOf(line: string): number {
  return line.length - line.replace(/^\s+/, "").length;
}

function dedent(line: string, n: number): string {
  let i = 0;
  while (i < n && i < line.length && (line[i] === " " || line[i] === "\t")) i++;
  return line.slice(i);
}

function isListItem(line: string): boolean {
  return LIST_ITEM.test(line);
}

function isHr(line: string): boolean {
  return /^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(line);
}

function isBlockStart(line: string): boolean {
  return (
    /^\s*```/.test(line) ||
    /^#{1,6}\s/.test(line) ||
    /^\s*>/.test(line) ||
    isHr(line) ||
    isListItem(line) ||
    isTableRow(line)
  );
}

function isTableRow(line: string): boolean {
  return /^\s*\|.*\|\s*$/.test(line);
}

/** A GFM delimiter row: `| --- | :--- | ---: |`. */
function isTableDelimiter(line: string): boolean {
  return isTableRow(line) && /^\s*\|(\s*:?-{3,}:?\s*\|)+\s*$/.test(line);
}

function splitRow(line: string): string[] {
  return line
    .trim()
    .replace(/^\||\|$/g, "")
    .split("|")
    .map((cell) => cell.trim());
}

/**
 * Render a pipe table starting at `start` (a header row followed by a delimiter
 * row). Cell counts are normalised to the header's, so a short or long body row
 * degrades into a well-formed table instead of broken HTML.
 */
function renderTable(lines: string[], start: number): [string, number] {
  const headers = splitRow(lines[start]);
  const cells = (row: string[]): string[] =>
    headers.map((_, idx) => (idx < row.length ? row[idx] : ""));

  const body: string[] = [];
  let i = start + 2;
  while (i < lines.length && isTableRow(lines[i]) && !isTableDelimiter(lines[i])) {
    const row = cells(splitRow(lines[i]))
      .map((cell) => `<td>${inline(cell)}</td>`)
      .join("");
    body.push(`<tr>${row}</tr>`);
    i++;
  }

  const head = headers.map((cell) => `<th>${inline(cell)}</th>`).join("");
  return [
    `<div class="dada-doc-tablewrap"><table class="dada-doc-table">` +
      `<thead><tr>${head}</tr></thead><tbody>${body.join("")}</tbody></table></div>`,
    i,
  ];
}

function sanitizeUrl(url: string): string {
  const trimmed = url.trim();
  if (/^javascript:/i.test(trimmed) || /^data:/i.test(trimmed) || /^vbscript:/i.test(trimmed)) {
    return "#";
  }
  if (/^https?:\/\//i.test(trimmed) || trimmed.startsWith("mailto:") || trimmed.startsWith("#")) {
    return trimmed;
  }
  return trimmed.replace(/\.md(#.*)?$/i, "$1");
}

function inline(raw: string): string {
  const codes: string[] = [];
  let text = raw.replace(/`([^`]+)`/g, (_m, code: string) => {
    codes.push(`<code class="dada-doc-code">${escapeHtml(code)}</code>`);
    return `${CODE_OPEN}${codes.length - 1}${CODE_CLOSE}`;
  });

  text = escapeHtml(text);

  text = text.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_m, label: string, url: string) => {
    const href = sanitizeUrl(url);
    const external = /^https?:\/\//i.test(href);
    const attrs = external ? ' target="_blank" rel="noopener noreferrer"' : "";
    return `<a href="${escapeHtml(href)}" class="dada-doc-link"${attrs}>${label}</a>`;
  });

  text = text.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  text = text.replace(/(^|[^*])\*([^*\s][^*]*?)\*(?!\*)/g, "$1<em>$2</em>");

  text = text.replace(new RegExp(`${CODE_OPEN}(\\d+)${CODE_CLOSE}`, "g"), (_m, i: string) => codes[Number(i)]);
  return text;
}

function renderItemBody(first: string, body: string[]): string {
  const cont: string[] = [];
  let k = 0;
  while (k < body.length && body[k].trim() !== "" && !isListItem(body[k])) {
    cont.push(body[k].trim());
    k++;
  }
  const text = inline([first, ...cont].join(" ").trim());
  const rest = body.slice(k);
  const nested = rest.length ? renderBlocks(rest) : "";
  return nested ? `${text}\n${nested}` : text;
}

function renderList(lines: string[], start: number, baseIndent: number): [string, number] {
  const items: string[] = [];
  let ordered: boolean | null = null;
  let i = start;

  while (i < lines.length) {
    const line = lines[i];
    if (line.trim() === "") break;
    if (indentOf(line) !== baseIndent) break;
    const m = line.match(LIST_ITEM);
    if (!m) break;
    const isOrdered = /\d/.test(m[2]);
    if (ordered === null) ordered = isOrdered;
    else if (ordered !== isOrdered) break;

    const contentIndent = m[1].length + m[2].length + m[3].length;
    const body: string[] = [];
    i++;
    while (i < lines.length && lines[i].trim() !== "" && indentOf(lines[i]) > baseIndent) {
      body.push(dedent(lines[i], contentIndent));
      i++;
    }
    items.push(`<li>${renderItemBody(m[4], body)}</li>`);
  }

  const tag = ordered ? "ol" : "ul";
  const cls = ordered ? "dada-doc-ol" : "dada-doc-ul";
  return [`<${tag} class="${cls}">${items.join("")}</${tag}>`, i];
}

function renderBlocks(lines: string[]): string {
  const out: string[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    if (line.trim() === "") {
      i++;
      continue;
    }

    const fence = line.match(/^\s*```(.*)$/);
    if (fence) {
      const code: string[] = [];
      i++;
      while (i < lines.length && !/^\s*```/.test(lines[i])) {
        code.push(lines[i]);
        i++;
      }
      i++;
      out.push(`<pre class="dada-doc-pre"><code>${escapeHtml(code.join("\n"))}</code></pre>`);
      continue;
    }

    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    if (heading) {
      const level = heading[1].length;
      out.push(`<h${level} class="dada-doc-h${level}">${inline(heading[2].trim())}</h${level}>`);
      i++;
      continue;
    }

    if (isHr(line)) {
      out.push('<hr class="dada-doc-hr" />');
      i++;
      continue;
    }

    if (/^\s*>/.test(line)) {
      const quote: string[] = [];
      while (i < lines.length && /^\s*>/.test(lines[i])) {
        quote.push(lines[i].replace(/^\s*>\s?/, ""));
        i++;
      }
      out.push(`<blockquote class="dada-doc-quote">${renderBlocks(quote)}</blockquote>`);
      continue;
    }

    if (isTableRow(line) && i + 1 < lines.length && isTableDelimiter(lines[i + 1])) {
      const [html, next] = renderTable(lines, i);
      out.push(html);
      i = next;
      continue;
    }

    if (isListItem(line)) {
      const [html, next] = renderList(lines, i, indentOf(line));
      out.push(html);
      i = next;
      continue;
    }

    const para: string[] = [];
    while (i < lines.length && lines[i].trim() !== "" && !isBlockStart(lines[i])) {
      para.push(lines[i].trim());
      i++;
    }
    out.push(`<p class="dada-doc-p">${inline(para.join(" "))}</p>`);
  }

  return out.join("\n");
}

/**
 * Render guide Markdown to an HTML string. HTML in the source is escaped; only
 * the supported Markdown constructs produce tags.
 */
export function renderMarkdown(markdown: string): string {
  const lines = markdown.replace(/\r\n/g, "\n").replace(/\t/g, "  ").split("\n");
  return renderBlocks(lines);
}
