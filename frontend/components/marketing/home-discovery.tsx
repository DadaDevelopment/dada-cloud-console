"use client";

import Link from "next/link";
import { ArrowRight, Check } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { consoleHref, localeHref } from "@/lib/site";
import { GOAL_LANDING_CTA, reachGoal } from "@/lib/metrika";
import styles from "@/app/(marketing)/home.module.css";

export function HomePricing() {
  const { t, locale } = useLang();
  const en = locale === "en";
  return (
    <section className={styles.section}>
      <div className={styles.container}>
        <div className={`${styles.sectionHeading} ${styles.pricingHeading}`}>
          <div>
            <p className={styles.kicker}>04 / {en ? "PLANS" : "ТАРИФЫ"}</p>
            <h2>{t.home.pricingTitle}</h2>
            <p>{t.home.pricingSubtitle}</p>
          </div>
          <Link
            className={styles.textLink}
            href={localeHref("/pricing", locale)}
          >
            {en ? "Compare plans" : "Сравнить тарифы"}
            <ArrowRight size={18} aria-hidden="true" />
          </Link>
        </div>
        <div className={styles.plans}>
          {t.home.pricingTiers.map((tier) => (
            <article
              key={tier.name}
              className={tier.highlight ? styles.featuredPlan : undefined}
            >
              <h3>{tier.name}</h3>
              <p className={styles.price}>{tier.price}</p>
              <p>{tier.tagline}</p>
              <ul>
                {tier.bullets.map((bullet) => (
                  <li key={bullet}>
                    <Check size={16} aria-hidden="true" />
                    {bullet}
                  </li>
                ))}
              </ul>
            </article>
          ))}
        </div>
        <div className={styles.planAction}>
          <Link
            href={consoleHref("/login")}
            className={styles.primary}
            onClick={() =>
              reachGoal(GOAL_LANDING_CTA, {
                source: "direct",
                placement: "pricing_teaser",
              })
            }
          >
            {t.common.createAccount}
            <ArrowRight size={18} aria-hidden="true" />
          </Link>
          <p>{t.home.pricingNote}</p>
        </div>
      </div>
    </section>
  );
}

export function HomeDirectory() {
  const { t, locale } = useLang();
  return (
    <section className={styles.directory}>
      <div className={styles.container}>
        <details>
          <summary>
            {t.home.hubTitle}
            <span aria-hidden="true">+</span>
          </summary>
          <p>{t.home.hubSubtitle}</p>
          <div className={styles.directoryColumns}>
            {[
              { title: t.footer.productsTitle, links: t.footer.products },
              { title: t.footer.hostingTitle, links: t.footer.hosting },
            ].map((column) => (
              <div key={column.title}>
                <h2>{column.title}</h2>
                <ul>
                  {column.links
                    .filter((link) => !link.href.startsWith("http"))
                    .map((link) => (
                      <li key={link.href}>
                        <Link href={localeHref(link.href, locale)}>
                          {link.label}
                        </Link>
                      </li>
                    ))}
                </ul>
              </div>
            ))}
          </div>
        </details>
      </div>
    </section>
  );
}
