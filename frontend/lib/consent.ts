"use client";

/**
 * Cookie/analytics consent — the 152-ФЗ half of web analytics.
 *
 * WHY THIS EXISTS. Yandex Metrika's own terms (п. 5.4 «Условия использования
 * сервиса Яндекс Метрика») name the site owner as the ОПЕРАТОР персональных
 * данных and Yandex only as the processor acting «по поручению» under ч. 3
 * ст. 6 152-ФЗ. Every obligation, and every fine under ст. 13.11 КоАП, is ours.
 * Processing needs a lawful basis; for analytics the only realistic one is
 * согласие (ст. 6 ч. 1 п. 1, ст. 9), and ст. 9 ч. 3 puts the burden of PROVING
 * that consent on the operator — which is why {@link journalConsent} exists.
 *
 * WHY OPT-IN AND NOT A NOTICE BAR. ФЗ от 24.06.2025 № 156-ФЗ amended ст. 9 ч. 1
 * with effect from 01.09.2025: consent must be «оформлено отдельно» from any
 * other document the visitor confirms. A «продолжая пользоваться сайтом, вы
 * соглашаетесь» bar is therefore not consent at all, and neither is a pre-ticked
 * box. The counter must not run until a distinct affirmative click.
 *
 * WHY THE COUNTER IS NOT MERELY DEFERRED. `ym(id, "init", {defer: true})` only
 * suppresses the automatic pageview hit — `tag.js` still loads and still sets
 * `_ym_uid`/`_ym_d`. The only real gate is not requesting the script at all,
 * which is what {@link loadYandexMetrika} implements: it is called from the
 * consent callback and nowhere else.
 *
 * WHY SELF-HOSTED. The library is an npm dependency bundled by Next and served
 * from our own origin. A CMP loaded from a foreign CDN would leak the visitor's
 * IP abroad before consent, and any SaaS CMP would write the consent record —
 * itself personal data — into an EU/US database, which is the ст. 18 ч. 5
 * localisation breach the whole exercise is meant to avoid.
 *
 * The `<noscript>` tracking pixel that normally accompanies the counter is
 * deliberately absent: it cannot be gated, so it would fire without consent.
 */
import type { CookieConsentConfig } from "vanilla-cookieconsent";
import { COMPANY } from "./company";
import { trackUxEvent } from "./ux-telemetry";

/**
 * Counter id, shared with {@link ./metrika.ts}. Public by construction — it
 * ships inside the page — so it lives in a NEXT_PUBLIC_ variable.
 */
const YM_ID = Number(process.env.NEXT_PUBLIC_YM_ID ?? "110158915");

/**
 * Bump when the Политика обработки ПДн or the set of categories changes in a
 * way that makes an earlier consent no longer cover current processing: every
 * visitor is then asked again. Kept in step with `revision` below.
 */
export const CONSENT_POLICY_VERSION = "2026-09-06";

/** Yandex's own opt-out extension, which Metrika terms п. 5.8 obliges us to publicise. */
const YM_BLOCKER_URL = "https://yandex.ru/support/metrica/general/opt-out.html";

const PRIVACY_URL_RU = "/privacy";
const PRIVACY_URL_EN = "/en/privacy";

let metrikaLoaded = false;

/**
 * Injects the Metrika counter. Idempotent, and never called before the
 * `analytics` category has been accepted.
 */
export function loadYandexMetrika(): void {
  if (typeof window === "undefined") return;
  if (metrikaLoaded) return;
  metrikaLoaded = true;
  const w = window as unknown as Record<string, unknown>;
  (function (m: Record<string, unknown>, e: Document, t: string, r: string, i: string) {
    const q = (m[i] ?? function (...args: unknown[]) {
      const fn = m[i] as { a?: unknown[] };
      (fn.a = fn.a || []).push(args);
    }) as { a?: unknown[]; l?: number };
    m[i] = q;
    q.l = 1 * Number(new Date());
    for (let j = 0; j < e.scripts.length; j++) {
      if (e.scripts[j].src === r) return;
    }
    const k = e.createElement(t) as HTMLScriptElement;
    const a = e.getElementsByTagName(t)[0];
    k.async = true;
    k.src = r;
    a.parentNode?.insertBefore(k, a);
  })(w, document, "script", "https://mc.yandex.ru/metrika/tag.js", "ym");
  const ym = w.ym as ((id: number, action: string, params?: unknown) => void) | undefined;
  ym?.(YM_ID, "init", {
    clickmap: true,
    trackLinks: true,
    accurateTrackBounce: true,
    webvisor: true,
    ecommerce: "dataLayer",
  });
}

/** Cookie prefixes Metrika sets, cleared by the library when consent is withdrawn. */
const METRIKA_COOKIES = [{ name: /^_ym/ }, { name: "yandexuid" }];

interface StoredConsent {
  consentId?: string;
  consentTimestamp?: string;
  lastConsentTimestamp?: string;
  categories?: string[];
  revision?: number;
  languageCode?: string;
}

/**
 * Writes the consent decision to `ux_events` — our own journal, on our own
 * Postgres in RF — so ст. 9 ч. 3 («обязанность доказать наличие согласия»)
 * can actually be discharged. The library keeps its record in a browser cookie
 * only, which the visitor can clear and which proves nothing to a regulator.
 *
 * Fail-open like the rest of the telemetry path: a dead ingest must never stop
 * the banner from working.
 */
export function journalConsent(cookie: StoredConsent, action: "grant" | "change"): void {
  try {
    trackUxEvent("consent", action, {
      consentId: cookie.consentId ?? "",
      categories: cookie.categories ?? [],
      analytics: (cookie.categories ?? []).includes("analytics"),
      consentTimestamp: cookie.consentTimestamp ?? "",
      lastConsentTimestamp: cookie.lastConsentTimestamp ?? "",
      revision: cookie.revision ?? 0,
      policyVersion: CONSENT_POLICY_VERSION,
    });
  } catch {
    return;
  }
}

/**
 * Banner and preferences copy.
 *
 * Each mandatory element maps to a norm РКН's crawler checks: the PURPOSE of
 * processing (анализ посещаемости), the CATEGORIES of data (cookie, IP-адрес,
 * устройство, действия на сайте), the RECIPIENT named explicitly (Яндекс
 * Метрика), and a link to the Политика on the banner itself (ст. 18.1 ч. 2).
 * «Принять все» and «Отклонить» carry equal visual weight, because ст. 9 ч. 1
 * requires consent given «свободно, своей волей» — a buried reject button is
 * not free consent.
 */
function translations(privacyUrl: string) {
  return {
    ru: {
      consentModal: {
        title: "Мы обрабатываем файлы cookie",
        description:
          `Сайт использует технически необходимые файлы cookie и, с вашего согласия, ` +
          `систему веб-аналитики Яндекс Метрика. Собираемые данные — файлы cookie, ` +
          `IP-адрес, сведения об устройстве и браузере, действия на страницах — ` +
          `обрабатываются в целях анализа посещаемости и улучшения работы сайта. ` +
          `Оператор — ${COMPANY.shortName}. Счётчик не загружается до вашего согласия. ` +
          `Нажимая «Принять все», вы даёте согласие на обработку персональных данных ` +
          `на условиях <a href="${privacyUrl}" class="cc__link">Политики обработки ` +
          `персональных данных</a>.`,
        acceptAllBtn: "Принять все",
        acceptNecessaryBtn: "Отклонить",
        showPreferencesBtn: "Настроить",
      },
      preferencesModal: {
        title: "Настройки обработки данных",
        acceptAllBtn: "Принять все",
        acceptNecessaryBtn: "Отклонить необязательные",
        savePreferencesBtn: "Сохранить выбор",
        closeIconLabel: "Закрыть",
        serviceCounterLabel: "сервис|сервисов",
        sections: [
          {
            title: "Какие данные мы обрабатываем",
            description:
              `Вы можете разрешить или запретить отдельные категории и изменить выбор ` +
              `в любой момент — согласие отзывается так же просто, как даётся ` +
              `(ч. 2 ст. 9 152-ФЗ). Оператор — ${COMPANY.shortName}, ИНН ${COMPANY.inn}, ` +
              `ОГРН ${COMPANY.ogrn}. Запросы по обработке данных — ${COMPANY.email}.`,
          },
          {
            title: "Технически необходимые",
            description:
              "Обеспечивают аутентификацию, безопасность и сохранение настроек. " +
              "Без них сайт не работает, поэтому отключить их нельзя.",
            linkedCategory: "necessary",
          },
          {
            title: "Аналитические — Яндекс Метрика",
            description:
              `Позволяют оценивать посещаемость и поведение пользователей, включая ` +
              `запись сессии (Вебвизор). Обработка осуществляется ООО «Яндекс» ` +
              `по поручению оператора (ч. 3 ст. 6 152-ФЗ) на серверах на территории ` +
              `Российской Федерации; трансграничная передача не осуществляется. ` +
              `Отказаться от сбора данных можно также с помощью ` +
              `<a href="${YM_BLOCKER_URL}" target="_blank" rel="noopener">Блокировщика ` +
              `Яндекс.Метрики</a>.`,
            linkedCategory: "analytics",
          },
        ],
      },
    },
    en: {
      consentModal: {
        title: "We process cookies",
        description:
          `This site uses strictly necessary cookies and, with your consent, the ` +
          `Yandex Metrika analytics service. The data collected — cookies, IP address, ` +
          `device and browser details, on-page actions — is processed to measure ` +
          `traffic and improve the site. Operator: ${COMPANY.shortNameEn}. The counter ` +
          `is not loaded until you consent. By clicking "Accept all" you consent to ` +
          `the processing of personal data on the terms of the ` +
          `<a href="${privacyUrl}" class="cc__link">Privacy Policy</a>.`,
        acceptAllBtn: "Accept all",
        acceptNecessaryBtn: "Reject",
        showPreferencesBtn: "Customise",
      },
      preferencesModal: {
        title: "Data processing settings",
        acceptAllBtn: "Accept all",
        acceptNecessaryBtn: "Reject optional",
        savePreferencesBtn: "Save choice",
        closeIconLabel: "Close",
        serviceCounterLabel: "service|services",
        sections: [
          {
            title: "What we process",
            description:
              `You can allow or refuse individual categories and change your choice at ` +
              `any time. Operator: ${COMPANY.shortNameEn}, INN ${COMPANY.inn}, ` +
              `OGRN ${COMPANY.ogrn}. Data requests: ${COMPANY.email}.`,
          },
          {
            title: "Strictly necessary",
            description:
              "Authentication, security and saved preferences. The site does not work " +
              "without them, so they cannot be switched off.",
            linkedCategory: "necessary",
          },
          {
            title: "Analytics — Yandex Metrika",
            description:
              `Traffic and behaviour measurement, including session recording (Webvisor). ` +
              `Processing is carried out by Yandex LLC on the operator's instruction ` +
              `(art. 6(3) of Federal Law 152-FZ) on servers located in the Russian ` +
              `Federation; no cross-border transfer takes place. You can also opt out ` +
              `with the <a href="${YM_BLOCKER_URL}" target="_blank" rel="noopener">Yandex ` +
              `Metrika blocker</a>.`,
            linkedCategory: "analytics",
          },
        ],
      },
    },
  };
}

/**
 * Builds the library configuration for a locale.
 *
 * `onConsent` — not `onFirstConsent` — is what starts the counter, because it
 * also fires on repeat visits where a stored consent already exists; the
 * visitor is asked once, not on every page load.
 */
export function consentConfig(locale: "ru" | "en"): CookieConsentConfig {
  const privacyUrl = locale === "en" ? PRIVACY_URL_EN : PRIVACY_URL_RU;
  return {
    guiOptions: {
      consentModal: { layout: "box", position: "bottom left", equalWeightButtons: true },
      preferencesModal: { layout: "box", equalWeightButtons: true },
    },
    categories: {
      necessary: { enabled: true, readOnly: true },
      analytics: {
        autoClear: { cookies: METRIKA_COOKIES, reloadPage: true },
      },
    },
    language: {
      default: locale,
      translations: translations(privacyUrl),
    },
    cookie: { name: "dada_cc", expiresAfterDays: 182, sameSite: "Lax" },
    revision: 1,
    onConsent: ({ cookie }) => {
      const stored = cookie as StoredConsent;
      if ((stored.categories ?? []).includes("analytics")) loadYandexMetrika();
      journalConsent(stored, "grant");
    },
    onChange: ({ cookie, changedCategories }) => {
      const stored = cookie as StoredConsent;
      if (changedCategories.includes("analytics") && (stored.categories ?? []).includes("analytics")) {
        loadYandexMetrika();
      }
      journalConsent(stored, "change");
    },
  };
}
