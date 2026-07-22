import { parseAnsi } from "@/lib/ansi";

/**
 * Renders a log line, interpreting ANSI SGR colour codes (as emitted by Rich,
 * pytest, npm, docker buildx, etc.) into styled spans. Without it the raw
 * escape codes leak into the DOM as unreadable `[31m`/`│[0m` noise. Lines with
 * no escape codes render as plain text, inheriting the parent colour.
 */
export function AnsiText({ value }: { value: string }) {
  const segments = parseAnsi(value);
  return (
    <>
      {segments.map((seg, i) =>
        seg.style ? (
          <span key={i} style={seg.style}>
            {seg.text}
          </span>
        ) : (
          seg.text
        )
      )}
    </>
  );
}
