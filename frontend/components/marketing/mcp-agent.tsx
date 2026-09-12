"use client";

import Link from "next/link";
import { ArrowRight, Check, MessageSquare } from "lucide-react";
import { useLang } from "@/lib/i18n/context";

type ChatLine = { role: "user" | "assistant"; text: string };

type McpCopy = {
  tag: string;
  title: string;
  subtitle: string;
  chat: ChatLine[];
  bullets: string[];
  cta: string;
};

/** The conversation is a labelled example, not a live agent session. */
export function McpAgentSection({ copy, href }: { copy: McpCopy; href: string }) {
  const { locale } = useLang();
  const isRu = locale === "ru";

  return (
    <section className="bg-slate-900 py-16 sm:py-20">
      <div className="mx-auto grid max-w-7xl items-center gap-10 px-4 sm:px-6 lg:grid-cols-2 lg:gap-16 lg:px-8">
        <div>
          <p className="mb-4 text-xs font-semibold uppercase tracking-wider text-blue-300">{copy.tag}</p>
          <h2 className="max-w-xl text-3xl font-semibold leading-tight tracking-tight text-white sm:text-4xl">
            {copy.title}
          </h2>
          <p className="mt-5 max-w-xl text-base leading-relaxed text-slate-300">{copy.subtitle}</p>
          <ul className="mt-6 space-y-3">
            {copy.bullets.map((bullet) => (
              <li key={bullet} className="flex items-start gap-3 text-sm text-slate-200">
                <Check className="mt-0.5 h-4 w-4 shrink-0 text-blue-400" aria-hidden="true" />
                {bullet}
              </li>
            ))}
          </ul>
          <Link
            href={href}
            className="mt-8 inline-flex items-center gap-3 rounded-lg bg-blue-600 px-6 py-3 text-sm font-semibold text-white transition-colors hover:bg-blue-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-400 focus-visible:ring-offset-2 focus-visible:ring-offset-slate-900"
          >
            {copy.cta}
            <ArrowRight className="h-4 w-4" aria-hidden="true" />
          </Link>
        </div>

        <figure className="overflow-hidden rounded-xl border border-slate-700 bg-slate-800/50">
          <figcaption className="flex items-center gap-3 border-b border-slate-700 px-5 py-4 text-sm text-slate-300">
            <MessageSquare className="h-4 w-4 text-blue-300" aria-hidden="true" />
            {isRu ? "Пример диалога · DADA Cloud MCP" : "Example conversation · DADA Cloud MCP"}
          </figcaption>
          <div className="space-y-5 p-5 sm:p-6">
            {copy.chat.map((line, index) => (
              <div key={index} className={line.role === "user" ? "ml-5 sm:ml-10" : "mr-5 sm:mr-10"}>
                <p className="mb-2 text-xs font-medium text-slate-400">
                  {line.role === "user" ? (isRu ? "Вы" : "You") : (isRu ? "Агент" : "Agent")}
                </p>
                <p className={`rounded-lg px-4 py-3 text-sm leading-relaxed ${line.role === "user" ? "bg-blue-600 text-white" : "border border-slate-600 bg-slate-800 text-slate-200"}`}>
                  {line.text}
                </p>
              </div>
            ))}
          </div>
        </figure>
      </div>
    </section>
  );
}
