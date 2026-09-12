"use client";

/** Consent and analytics behaviour live in lib/consent; only marketing presentation differs. */
import { useEffect } from "react";
import { useSelectedLayoutSegments } from "next/navigation";
import type { Translation } from "vanilla-cookieconsent";
import "vanilla-cookieconsent/dist/cookieconsent.css";
import "./marketing-cookie-consent.css";
import { consentConfig } from "@/lib/consent";
import { COMPANY } from "@/lib/company";

function marketingTranslation(translation: Translation, locale: "ru" | "en"): Translation {
  const privacyUrl = locale === "ru" ? "/privacy" : "/en/privacy";
  return {
    ...translation,
    consentModal: {
      ...translation.consentModal,
      title: locale === "ru" ? "Cookie — ваш выбор" : "Cookies — your choice",
      description: locale === "ru"
        ? `Необходимые cookie помогают сайту работать. С вашего согласия ${COMPANY.shortName} использует Яндекс Метрику: cookie, IP-адрес, данные устройства и браузера, действия на сайте — для анализа посещаемости и улучшения сайта. «Принять все» — согласие на обработку данных по <a href="${privacyUrl}" class="cc__link">Политике конфиденциальности</a>.`
        : `Necessary cookies keep the site working. With your consent, ${COMPANY.shortNameEn} uses Yandex Metrika: cookies, IP address, device and browser details, and on-page actions to measure traffic and improve the site. “Accept all” consents to processing under the <a href="${privacyUrl}" class="cc__link">Privacy Policy</a>.`,
    },
    preferencesModal: {
      ...translation.preferencesModal,
      sections: [
        {
          title: translation.consentModal.title,
          description: translation.consentModal.description,
        },
        ...translation.preferencesModal.sections,
      ],
    },
  };
}

export function CookieConsent({ lang }: { lang?: string }) {
  const locale = lang?.toLowerCase().startsWith("ru") ? "ru" : "en";
  // Route groups are included by this hook, including on localhost and nested EN pages.
  const isMarketing = useSelectedLayoutSegments().includes("(marketing)");

  useEffect(() => {
    let cancelled = false;
    const root = document.documentElement;
    root.toggleAttribute("data-marketing-consent", isMarketing);

    import("vanilla-cookieconsent").then(async (cc) => {
      if (cancelled) return;
      const config = consentConfig(locale);
      for (const language of ["ru", "en"] as const) {
        const original = config.language.translations[language];
        if (typeof original !== "object") continue;
        // Read the current route when the library refreshes its translation,
        // so navigating into the console restores the original presentation.
        config.language.translations[language] = () =>
          root.hasAttribute("data-marketing-consent")
            ? marketingTranslation(original, language)
            : original;
      }
      await cc.run(config);
      if (!cancelled) await cc.setLanguage(locale, true);
    });

    return () => {
      cancelled = true;
      root.removeAttribute("data-marketing-consent");
    };
  }, [locale, isMarketing]);
  return null;
}
