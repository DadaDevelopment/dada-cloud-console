import { ImageResponse } from "next/og";

/**
 * Shared Open Graph card renderer.
 *
 * Every page used to point `og:image` at the one static /og.png — the home
 * page's banner, headline and all. A doc article shared in a chat therefore
 * unfurled as an ad for the landing page, which is both wrong and useless for
 * deciding whether to open the link. This draws the page's own headline in the
 * same layout instead.
 *
 * No custom font is loaded: the default face @vercel/og ships is Geist, and it
 * covers Cyrillic, so RU headlines render rather than showing tofu.
 */

export const OG_SIZE = { width: 1200, height: 630 };
export const OG_CONTENT_TYPE = "image/png";

const LOGO =
  "data:image/svg+xml;base64," +
  Buffer.from(
    `<svg xmlns="http://www.w3.org/2000/svg" width="512" height="512" viewBox="0 0 512 512"><rect width="512" height="512" rx="112" fill="#2563eb"/><g transform="translate(77,77) scale(15.76)" fill="none" stroke="#ffffff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z"/></g></svg>`,
  ).toString("base64");

export interface OgCardProps {
  /** Small pill above the headline — the section the page belongs to. */
  eyebrow?: string;
  /** The headline. Long ones step down a size rather than overflow. */
  title: string;
  /** One supporting line under the headline. Trimmed when very long. */
  subtitle?: string;
  /** Right-hand footer note; the left side is always the site host. */
  footerNote?: string;
}

function titleFontSize(title: string): number {
  if (title.length > 90) return 52;
  if (title.length > 60) return 62;
  if (title.length > 38) return 72;
  return 84;
}

function clamp(text: string, max: number): string {
  return text.length > max ? `${text.slice(0, max - 1).trimEnd()}…` : text;
}

export function ogCard({ eyebrow, title, subtitle, footerNote }: OgCardProps): ImageResponse {
  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          flexDirection: "column",
          justifyContent: "space-between",
          padding: "72px 80px",
          background: "linear-gradient(135deg, #0b1220 0%, #111f3d 55%, #16305e 100%)",
          fontFamily: "Geist, sans-serif",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 20 }}>
          <img src={LOGO} width={56} height={56} alt="" />
          <span style={{ fontSize: 34, fontWeight: 700, color: "#ffffff", letterSpacing: -0.5 }}>
            DADA Cloud
          </span>
        </div>

        <div style={{ display: "flex", flexDirection: "column", gap: 28 }}>
          {eyebrow ? (
            <div style={{ display: "flex" }}>
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 12,
                  padding: "10px 22px",
                  borderRadius: 999,
                  border: "1px solid #2b4a86",
                  background: "#132a52",
                  color: "#9dc0ff",
                  fontSize: 24,
                  fontWeight: 600,
                }}
              >
                <div style={{ width: 10, height: 10, borderRadius: 999, background: "#3b82f6" }} />
                {clamp(eyebrow, 52)}
              </div>
            </div>
          ) : null}

          <div
            style={{
              display: "flex",
              fontSize: titleFontSize(title),
              lineHeight: 1.12,
              fontWeight: 700,
              color: "#ffffff",
              letterSpacing: -1.5,
            }}
          >
            {clamp(title, 120)}
          </div>

          {subtitle ? (
            <div style={{ display: "flex", fontSize: 30, lineHeight: 1.35, color: "#93a4c4" }}>
              {clamp(subtitle, 140)}
            </div>
          ) : null}
        </div>

        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            borderTop: "1px solid #223257",
            paddingTop: 26,
            fontSize: 24,
            color: "#7d8db0",
          }}
        >
          <span>cloud.dada-tuda.ru</span>
          {footerNote ? <span>{clamp(footerNote, 46)}</span> : null}
        </div>
      </div>
    ),
    OG_SIZE,
  );
}
