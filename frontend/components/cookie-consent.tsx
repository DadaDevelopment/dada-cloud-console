"use client";

/**
 * Mounts the consent banner and, through it, gates Yandex Metrika.
 *
 * Client-only and effect-driven: the library manipulates `document` directly,
 * so it must not run during SSR. Rendering nothing keeps the server markup
 * identical for consenting and non-consenting visitors, which avoids a
 * hydration mismatch on the statically rendered marketing pages.
 *
 * See {@link ../lib/consent} for the legal rationale behind opt-in gating.
 */
import { useEffect } from "react";
import "vanilla-cookieconsent/dist/cookieconsent.css";
import { consentConfig } from "@/lib/consent";

export function CookieConsent({ lang }: { lang?: string }) {
  const locale = lang?.toLowerCase().startsWith("ru") ? "ru" : "en";
  useEffect(() => {
    let cancelled = false;
    import("vanilla-cookieconsent").then((cc) => {
      if (cancelled) return;
      cc.run(consentConfig(locale));
    });
    return () => {
      cancelled = true;
    };
  }, [locale]);
  return null;
}
