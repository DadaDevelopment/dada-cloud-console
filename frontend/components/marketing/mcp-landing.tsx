"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, StepsGrid, FeatureGrid, FaqList, CtaBand, RelatedLinks } from "./sections";
import { FaqJsonLd } from "./faq-jsonld";
import { HowToJsonLd } from "./howto-jsonld";

const UTM = "utm_source=mcp_landing";
const MCP_URL = "https://console.dada-tuda.ru/mcp";

const connectBlocks = (isEn: boolean) => [
  {
    client: "Claude Code",
    lines: ["/plugin marketplace add DadaDevelopment/dada-cloud-console", "/plugin install dada-cloud@dada-cloud"],
  },
  {
    client: `Claude Desktop · Cursor · ${isEn ? "any MCP client" : "любой MCP-клиент"}`,
    lines: [MCP_URL, "OAuth Client ID: dada-mcp"],
  },
];

/**
 * `/mcp` and `/en/mcp`. A guessable, Russian-language entry point for the MCP
 * server: the guide at `/developer/mcp-ai-agents` is buried in a list, so anyone
 * told "DADA has an MCP" had nowhere to land.
 */
export function McpLanding() {
  const { t, locale } = useLang();
  const g = t.mcpAlt;
  const isEn = locale === "en";
  const connectTitle = isEn ? "Connection details" : "Реквизиты подключения";
  const connectSubtitle = isEn
    ? "Copy these into your client — nothing else to configure."
    : "Скопируйте в клиент — больше ничего настраивать не нужно.";

  return (
    <>
      <FaqJsonLd path="/mcp" items={g.faq} />
      <HowToJsonLd
        path="/mcp"
        name={g.heroTitle}
        description={g.heroSubtitle}
        steps={g.steps.map((s) => ({ name: s.title, text: s.desc }))}
      />
      <ProductHero title={g.heroTitle} subtitle={g.heroSubtitle} ctaHref={`/register?${UTM}`} />

      <section className="bg-white py-16">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-10 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">{connectTitle}</h2>
            <p className="mt-3 text-lg text-slate-600">{connectSubtitle}</p>
          </div>
          <div className="grid gap-6 md:grid-cols-2">
            {connectBlocks(isEn).map((b) => (
              <div key={b.client} className="rounded-xl border border-slate-200 bg-slate-50 p-6">
                <h3 className="text-sm font-semibold uppercase tracking-wide text-slate-500">{b.client}</h3>
                <div className="mt-4 space-y-2">
                  {b.lines.map((line) => (
                    <pre
                      key={line}
                      className="overflow-x-auto rounded-lg bg-[#0b1220] px-4 py-3 text-sm text-slate-100"
                    >
                      <code>{line}</code>
                    </pre>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>
      </section>

      <StepsGrid title={g.stepsTitle} subtitle={g.stepsSubtitle} steps={g.steps} />
      <FeatureGrid title={g.featuresTitle} features={g.features} />
      <FaqList title={g.faqTitle} items={g.faq} />

      <RelatedLinks
        links={[
          { label: isEn ? "Full MCP guide" : "Полное руководство по MCP", href: "/developer/mcp-ai-agents" },
          { label: isEn ? "REST API and docs" : "REST API и документация", href: "/developer" },
          { label: isEn ? "Pricing" : "Тарифы", href: "/pricing" },
        ]}
      />

      <CtaBand ctaHref={`/register?${UTM}`} />
    </>
  );
}
