"use client";

import Link from "next/link";
import { useLang } from "@/lib/i18n/context";
import { consoleHref } from "@/lib/site";
import { ProductHero, CtaBand } from "@/components/marketing/sections";

export default function DeveloperPage() {
  const { locale } = useLang();
  const copy = {
    ru: {
      title: "Документация и API",
      subtitle: "Управляйте инфраструктурой через REST API и CLI. Полная документация платформы скоро.",
      apiTitle: "REST API",
      apiDesc: "Все ресурсы консоли доступны через /api/v1 с авторизацией по токену.",
      consoleTitle: "Открыть консоль",
      consoleDesc: "Войдите, чтобы получить токен доступа и kubeconfig.",
    },
    en: {
      title: "Documentation & API",
      subtitle: "Manage your infrastructure via REST API and CLI. Full platform docs are coming soon.",
      apiTitle: "REST API",
      apiDesc: "Every console resource is available via /api/v1 with token auth.",
      consoleTitle: "Open console",
      consoleDesc: "Log in to get an access token and kubeconfig.",
    },
  }[locale];

  return (
    <>
      <ProductHero title={copy.title} subtitle={copy.subtitle} />
      <section className="bg-white py-20">
        <div className="mx-auto grid max-w-5xl gap-6 px-4 sm:px-6 md:grid-cols-2 lg:px-8">
          <div className="rounded-xl border border-slate-200 bg-white p-7">
            <h3 className="text-lg font-semibold text-slate-900">{copy.apiTitle}</h3>
            <p className="mt-2 text-sm text-slate-600">{copy.apiDesc}</p>
            <pre className="mt-4 overflow-x-auto rounded-lg bg-slate-900 p-4 text-xs text-slate-100">
{`curl -H "Authorization: Bearer $TOKEN" \\
  https://api.dada.cloud/api/v1/projects`}
            </pre>
          </div>
          <Link href={consoleHref("/projects")} className="rounded-xl border border-slate-200 bg-white p-7 transition-shadow hover:shadow-md">
            <h3 className="text-lg font-semibold text-slate-900">{copy.consoleTitle}</h3>
            <p className="mt-2 text-sm text-slate-600">{copy.consoleDesc}</p>
          </Link>
        </div>
      </section>
      <CtaBand />
    </>
  );
}
