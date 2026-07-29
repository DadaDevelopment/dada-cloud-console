"use client";

import { useId, useState } from "react";
import Link from "next/link";
import { AlertTriangle, ArrowRight, Check, Loader2, Sparkles } from "lucide-react";
import { clsx } from "clsx";
import type { BoxCopy } from "@/lib/box-copy";
import { reportCrystallizeIntent, submitBoxLead } from "@/lib/box-events";
import { localeHref } from "@/lib/site";

/**
 * The door itself: captures intent, returns a real request code, then asks the
 * highest-signal question (do you need crystallization, and which parts).
 *
 * Honesty rules this component must keep (docs/product/box-product-brief.md §6):
 * the success state says access is granted by hand, and it never invents a queue
 * position or shows a box that does not exist.
 */

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export function BoxAccessForm({ copy, locale }: { copy: BoxCopy; locale: string }) {
  const c = copy.form;
  const uid = useId();

  const [email, setEmail] = useState("");
  const [contact, setContact] = useState("");
  const [agent, setAgent] = useState(c.agentOptions[0]);
  const [parallel, setParallel] = useState(c.parallelOptions[0]);
  const [useCase, setUseCase] = useState("");
  const [price, setPrice] = useState(c.priceOptions[0]);

  const [status, setStatus] = useState<"idle" | "sending" | "done">("idle");
  const [error, setError] = useState<string | null>(null);
  const [claim, setClaim] = useState<string | null>(null);

  const [wants, setWants] = useState<string[]>([]);
  const [crystalSent, setCrystalSent] = useState(false);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (status === "sending") return;

    if (!email.trim() || !useCase.trim()) {
      setError(c.errorRequired);
      return;
    }
    if (!EMAIL_RE.test(email.trim())) {
      setError(c.errorEmail);
      return;
    }

    setError(null);
    setStatus("sending");
    try {
      const { claim: code } = await submitBoxLead({
        email: email.trim(),
        contact: contact.trim() || undefined,
        agent,
        parallel,
        useCase: useCase.trim(),
        price,
        locale,
      });
      setClaim(code);
      setStatus("done");
    } catch {
      setError(c.errorGeneric);
      setStatus("idle");
    }
  }

  function toggleWant(option: string) {
    setWants((prev) =>
      prev.includes(option) ? prev.filter((w) => w !== option) : [...prev, option],
    );
  }

  function sendCrystalIntent() {
    if (!claim || crystalSent) return;
    reportCrystallizeIntent(claim, wants, locale);
    setCrystalSent(true);
  }

  return (
    <section id="access" className="scroll-mt-20 bg-slate-50 py-20">
      <div className="mx-auto max-w-2xl px-4 sm:px-6 lg:px-8">
        <h2 className="text-3xl font-bold tracking-tight text-slate-900">{c.title}</h2>
        <p className="mt-3 text-lg text-slate-600">{c.subtitle}</p>

        {status === "done" && claim ? (
          <div className="mt-8 space-y-6">
            <div className="rounded-xl border border-emerald-300 bg-emerald-50 p-6">
              <div className="flex items-start gap-3">
                <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-emerald-600 text-white">
                  <Check className="h-4 w-4" />
                </span>
                <div>
                  <h3 className="text-base font-semibold text-emerald-900">{c.successTitle}</h3>
                  <p className="mt-2 text-sm text-emerald-900/80">{c.successBody}</p>
                  <p className="mt-4 text-xs font-semibold uppercase tracking-wide text-emerald-900/60">
                    {c.claimLabel}
                  </p>
                  <code className="mt-1 inline-block rounded-md border border-emerald-300 bg-white px-3 py-1.5 font-mono text-sm text-emerald-900">
                    {claim}
                  </code>
                </div>
              </div>
            </div>

            <div className="rounded-xl border border-slate-200 bg-white p-6">
              <div className="flex items-center gap-2">
                <Sparkles className="h-4 w-4 text-blue-600" />
                <h3 className="text-base font-semibold text-slate-900">{c.crystalTitle}</h3>
              </div>
              <p className="mt-2 text-sm text-slate-600">{c.crystalBody}</p>
              <div className="mt-4 space-y-2">
                {c.crystalOptions.map((option) => (
                  <label
                    key={option}
                    className={clsx(
                      "flex cursor-pointer items-start gap-3 rounded-lg border p-3 text-sm transition-colors",
                      wants.includes(option)
                        ? "border-blue-500 bg-blue-50 text-slate-900"
                        : "border-slate-200 bg-white text-slate-700 hover:border-slate-300",
                      crystalSent && "cursor-default opacity-70",
                    )}
                  >
                    <input
                      type="checkbox"
                      className="mt-0.5 h-4 w-4 accent-blue-600"
                      checked={wants.includes(option)}
                      onChange={() => toggleWant(option)}
                      disabled={crystalSent}
                    />
                    <span>{option}</span>
                  </label>
                ))}
              </div>
              {crystalSent ? (
                <p className="mt-4 flex items-center gap-2 text-sm font-medium text-emerald-700">
                  <Check className="h-4 w-4" />
                  {c.crystalDone}
                </p>
              ) : (
                <button
                  type="button"
                  onClick={sendCrystalIntent}
                  disabled={wants.length === 0}
                  className="mt-4 inline-flex items-center gap-2 rounded-md bg-blue-600 px-5 py-2.5 text-sm font-semibold text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:bg-slate-300"
                >
                  {c.crystalSubmit}
                  <ArrowRight className="h-4 w-4" />
                </button>
              )}
            </div>
          </div>
        ) : (
          <form onSubmit={onSubmit} className="mt-8 space-y-5" noValidate>
            <Field id={`${uid}-email`} label={c.emailLabel} required>
              <input
                id={`${uid}-email`}
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder={c.emailPlaceholder}
                autoComplete="email"
                className={inputClass}
              />
            </Field>

            <Field id={`${uid}-contact`} label={c.contactLabel}>
              <input
                id={`${uid}-contact`}
                type="text"
                value={contact}
                onChange={(e) => setContact(e.target.value)}
                placeholder={c.contactPlaceholder}
                className={inputClass}
              />
            </Field>

            <div className="grid gap-5 sm:grid-cols-2">
              <Field id={`${uid}-agent`} label={c.agentLabel}>
                <select
                  id={`${uid}-agent`}
                  value={agent}
                  onChange={(e) => setAgent(e.target.value)}
                  className={inputClass}
                >
                  {c.agentOptions.map((o) => (
                    <option key={o} value={o}>
                      {o}
                    </option>
                  ))}
                </select>
              </Field>

              <Field id={`${uid}-parallel`} label={c.parallelLabel}>
                <select
                  id={`${uid}-parallel`}
                  value={parallel}
                  onChange={(e) => setParallel(e.target.value)}
                  className={inputClass}
                >
                  {c.parallelOptions.map((o) => (
                    <option key={o} value={o}>
                      {o}
                    </option>
                  ))}
                </select>
              </Field>
            </div>

            <Field id={`${uid}-usecase`} label={c.useCaseLabel} required>
              <textarea
                id={`${uid}-usecase`}
                value={useCase}
                onChange={(e) => setUseCase(e.target.value)}
                placeholder={c.useCasePlaceholder}
                rows={3}
                className={inputClass}
              />
            </Field>

            <Field id={`${uid}-price`} label={c.priceLabel}>
              <select
                id={`${uid}-price`}
                value={price}
                onChange={(e) => setPrice(e.target.value)}
                className={inputClass}
              >
                {c.priceOptions.map((o) => (
                  <option key={o} value={o}>
                    {o}
                  </option>
                ))}
              </select>
            </Field>

            {error && (
              <p role="alert" className="flex items-center gap-2 text-sm font-medium text-red-600">
                <AlertTriangle className="h-4 w-4 shrink-0" />
                {error}
              </p>
            )}

            <button
              type="submit"
              disabled={status === "sending"}
              className="inline-flex items-center gap-2 rounded-md bg-blue-600 px-6 py-3 text-sm font-semibold text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:bg-blue-400"
            >
              {status === "sending" ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" />
                  {c.submitting}
                </>
              ) : (
                <>
                  {c.submit}
                  <ArrowRight className="h-4 w-4" />
                </>
              )}
            </button>

            <p className="text-xs text-slate-400">
              {c.privacy}{" "}
              <Link
                href={localeHref("/privacy", locale === "en" ? "en" : "ru")}
                className="underline hover:text-slate-600"
              >
                {c.privacyLink}
              </Link>
            </p>
          </form>
        )}
      </div>
    </section>
  );
}

const inputClass =
  "w-full rounded-md border border-slate-300 bg-white px-3.5 py-2.5 text-sm text-slate-900 shadow-sm outline-none transition-colors placeholder:text-slate-400 focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20";

function Field({
  id,
  label,
  required,
  children,
}: {
  id: string;
  label: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div>
      <label htmlFor={id} className="mb-1.5 block text-sm font-medium text-slate-800">
        {label}
        {required && <span className="ml-1 text-red-500">*</span>}
      </label>
      {children}
    </div>
  );
}
