"use client";

import Link from "next/link";
import { Cloud } from "lucide-react";
import { COMPANY } from "@/lib/company";
import { useLang } from "@/lib/i18n/context";
import { localeHref } from "@/lib/site";

// Marketing routes that have a localized "/en" mirror. Console links
// ("/projects", "/login") must NOT be locale-prefixed.
const MARKETING_PATHS = new Set([
  "/",
  "/cloud-servers",
  "/databases",
  "/storage",
  "/pricing",
  "/analog-vercel",
  "/oplatit-vercel-iz-rossii",
  "/rabotaet-li-vercel-v-rossii",
  "/analog-heroku",
  "/analog-railway",
  "/analog-render",
  "/analog-netlify",
  "/analog-digitalocean",
  "/analog-fly-io",
  "/deploy-vibe-coding",
  "/deploy-without-git",
  "/hosting-telegram-bot",
  "/deploy-aiogram-bot",
  "/hosting-discord-bot",
  "/hosting-vk-bot",
  "/telegram-agent-gateway",
  "/hosting-fastapi",
  "/hosting-flask",
  "/hosting-django",
  "/hosting-streamlit",
  "/accept-payments",
  "/migrate-vercel",
  "/status",
  "/developer",
  "/mcp",
  "/developer/mcp-ai-agents",
  "/privacy",
  "/terms",
  "/company",
]);

export function MarketingFooter() {
  const { t, locale } = useLang();
  const href = (path: string) => (MARKETING_PATHS.has(path) ? localeHref(path, locale) : path);
  const cols = [
    { title: t.footer.productsTitle, links: t.footer.products },
    { title: t.footer.hostingTitle, links: t.footer.hosting },
    { title: t.footer.companyTitle, links: t.footer.company },
    { title: t.footer.resourcesTitle, links: t.footer.resources },
  ];

  return (
    <footer className="border-t border-white/10 bg-[#0b1220] text-white/70">
      <div className="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
        <div className="grid grid-cols-2 gap-8 md:grid-cols-3 lg:grid-cols-5">
          <div className="col-span-2 md:col-span-3 lg:col-span-1">
            <Link href={localeHref("/", locale)} className="flex items-center gap-2 text-white">
              <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-blue-600">
                <Cloud className="h-5 w-5" />
              </span>
              <span className="text-lg font-bold">DADA Cloud</span>
            </Link>
            <p className="mt-3 max-w-xs text-sm">{t.footer.tagline}</p>
          </div>
          {cols.map((col) => (
            <div key={col.title}>
              <h3 className="text-sm font-semibold text-white">{col.title}</h3>
              <ul className="mt-3 space-y-2 text-sm">
                {col.links.map((l) => (
                  <li key={l.label + l.href}>
                    {l.href.startsWith("http") ? (
                      <a
                        href={l.href}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="transition-colors hover:text-white"
                      >
                        {l.label}
                      </a>
                    ) : (
                      <Link href={href(l.href)} className="transition-colors hover:text-white">
                        {l.label}
                      </Link>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
        <div className="mt-10 flex flex-col items-start justify-between gap-2 border-t border-white/10 pt-6 text-xs sm:flex-row sm:items-center">
          <span className="max-w-2xl">
            © 2026 DADA Cloud. {t.footer.rights}
            <br />
            {COMPANY.shortName}, ИНН {COMPANY.inn}, ОГРН {COMPANY.ogrn}, {COMPANY.legalAddress}
          </span>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
            {t.footer.legalLinks.map((l) => (
              <Link key={l.href} href={href(l.href)} className="transition-colors hover:text-white">
                {l.label}
              </Link>
            ))}
            <button
              type="button"
              data-cc="show-preferencesModal"
              className="transition-colors hover:text-white"
            >
              {locale === "ru" ? "Настройки cookie" : "Cookie settings"}
            </button>
          </div>
        </div>
      </div>
    </footer>
  );
}
