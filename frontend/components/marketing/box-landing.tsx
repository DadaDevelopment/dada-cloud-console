"use client";

import { useEffect } from "react";
import Link from "next/link";
import { ArrowDown, ArrowRight, Box, Code2, Globe, Laptop, Terminal } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { BOX_UTM_SOURCE, reportBoxPageView } from "@/lib/box-events";
import { boxCopy } from "@/lib/box-copy";
import { localeHref } from "@/lib/site";
import { BoxDemo } from "./box-demo";
import { BoxConnect } from "./box-connect";
import { BoxAccessForm } from "./box-access-form";
import { FaqJsonLd } from "./faq-jsonld";
import styles from "./agent-products.module.css";

function ctaHref(locale: "ru" | "en", hash: string): string {
  return `${localeHref("/box", locale)}?utm_source=${BOX_UTM_SOURCE}#${hash}`;
}

export function BoxLanding() {
  const { locale } = useLang();
  const copy = boxCopy[locale];
  const isRu = locale === "ru";
  const connectHref = ctaHref(locale, "connect");
  const helpHref = ctaHref(locale, "access");
  // The historical replay also includes experimental VM promotion. Keep the
  // working Box illustration separate from the explicitly qualified VM details.
  const promotionIndex = copy.demo.lines.findIndex((line) => line.kind === "cmd" && line.text.includes("crystallize"));
  const demoLines = promotionIndex < 0 ? copy.demo.lines : copy.demo.lines.slice(0, promotionIndex);

  useEffect(() => {
    reportBoxPageView(locale);
  }, [locale]);

  const steps = isRu ? [
    { title: "Подключите агента", text: "Добавьте DADA Cloud в Claude Code или другой MCP-клиент. Войдите в свой аккаунт через браузер." },
    { title: "Выделите ему Box", text: "Агент создаст отдельное окружение для задачи. Код, зависимости и сборка будут выполняться в облаке." },
    { title: "Покажите результат", text: "Откройте порт приложения по HTTPS, чтобы поделиться прототипом. Состояние бокса видно в консоли." },
  ] : [
    { title: "Connect your agent", text: "Add DADA Cloud to Claude Code or another MCP client. Sign in to your account through the browser." },
    { title: "Give it a Box", text: "Your agent creates a separate environment for the task. Code, dependencies and builds run in the cloud." },
    { title: "Share the result", text: "Expose an app port over HTTPS to share your prototype. Check the box status in the console." },
  ];

  return (
    <div className={styles.page}>
      <FaqJsonLd path="/box" items={copy.faq.items} />
      <section className={styles.hero}>
        <div className={`${styles.wrap} ${styles.heroGrid}`}>
          <div>
            <p className={styles.eyebrow}>DADA BOX <span /> {copy.badge}</p>
            <h1 className={styles.title}>{copy.heroTitle}</h1>
            <p className={styles.lead}>{copy.heroSubtitle}</p>
            <div className={styles.actions}>
              <Link href={connectHref} data-ux="box_connect:hero_cta" className={styles.primary}>
                {copy.heroPrimary}<ArrowRight size={17} aria-hidden="true" />
              </Link>
              <Link href={ctaHref(locale, "how")} className={styles.secondary}>{copy.heroSecondary}</Link>
            </div>
            <p className={styles.note}>{copy.heroNote}</p>
          </div>
          <div className={styles.diagram} aria-label={isRu ? "Ваш агент выполняет задачи в отдельном облачном компьютере" : "Your agent runs tasks on a separate cloud computer"}>
            <p className={styles.diagramLabel}>{isRu ? "Ваш агент. Отдельное окружение." : "Your agent. A separate environment."}</p>
            <div className={styles.agentNode}><Laptop size={18} aria-hidden="true" />Claude · Cursor · Codex</div>
            <div className={styles.connector}><ArrowDown size={21} aria-hidden="true" /></div>
            <div className={styles.cloudNode}>
              <div className={styles.nodeHead}>
                <span className={styles.nodeIcon}><Box size={25} aria-hidden="true" /></span>
                <div><strong>DADA Box</strong><small>{isRu ? "Компьютер в облаке · root-доступ" : "Cloud computer · root access"}</small></div>
              </div>
              <div className={styles.nodeResources}>
                <span><Code2 size={13} aria-hidden="true" />{isRu ? "Код и файлы" : "Code and files"}</span>
                <span><Terminal size={13} aria-hidden="true" />{isRu ? "Команды и сборка" : "Commands and builds"}</span>
                <span><Globe size={13} aria-hidden="true" />HTTPS</span>
              </div>
            </div>
            <p className={styles.diagramFoot}>{isRu ? "Задачи выполняются здесь. Ваш ноутбук свободен." : "Tasks run here. Your laptop stays free."}</p>
          </div>
        </div>
      </section>

      <section id="how" className={`${styles.section} ${styles.sectionWhite} scroll-mt-24`}>
        <div className={styles.wrap}>
          <div className={styles.sectionHead}>
            <h2 className={styles.heading}>{isRu ? "От задачи до ссылки на результат" : "From a task to a link you can share"}</h2>
            <p className={styles.description}>{isRu ? "Для прототипов, сборок и экспериментов, которым тесно на рабочей машине." : "For prototypes, builds and experiments that need room beyond your own machine."}</p>
          </div>
          <div className={styles.steps}>{steps.map((step, i) => (
            <div key={step.title} className={styles.step}><span className={styles.stepNumber}>0{i + 1}</span><h3>{step.title}</h3><p>{step.text}</p></div>
          ))}</div>
        </div>
      </section>

      <div className={styles.embedded}><BoxConnect copy={copy.connect} helpHref={helpHref} /></div>

      <section className={styles.section}>
        <div className={styles.wrap}>
          <div className={styles.sectionHead}><h2 className={styles.heading}>{copy.pricing.title}</h2><p className={styles.description}>{copy.pricing.subtitle}</p></div>
          {copy.pricing.tiers.slice(0, 2).map((tier) => (
            <div key={tier.name} className={styles.priceRow}><h3>{tier.name}</h3><strong>{tier.price}</strong><p>{tier.note}</p></div>
          ))}
          <p className={styles.note}>{copy.pricing.disclaimer} <Link href={localeHref("/pricing", locale)} className="font-semibold text-blue-600 hover:underline">{isRu ? "Все тарифы →" : "All plans →"}</Link></p>
          <p className={styles.limit}>{copy.crystal.note}</p>
        </div>
      </section>

      <section className={`${styles.section} ${styles.sectionWhite}`}>
        <div className={styles.wrap}>
          <div className={styles.sectionHead}><h2 className={styles.heading}>{isRu ? "Что стоит знать" : "Before you start"}</h2><p className={styles.description}>{isRu ? "Возможности, ограничения и ответы без мелкого шрифта." : "Capabilities, limits and answers without the fine print."}</p></div>
          <div className={styles.detailsGrid}>
            {copy.faq.items.map((item) => <details className={styles.detail} key={item.q}><summary>{item.q}</summary><div className={styles.detailBody}><p>{item.a}</p></div></details>)}
            <details className={styles.detail}>
              <summary>{copy.honesty.title}</summary>
              <div className={styles.detailBody}>
                <p>{copy.honesty.subtitle}</p>
                <h3>{copy.honesty.worksTitle}</h3><ul>{copy.honesty.works.map((item) => <li key={item}>{item}</li>)}</ul>
                <h3>{copy.honesty.notYetTitle}</h3><ul>{copy.honesty.notYet.map((item) => <li key={item}>{item}</li>)}</ul>
              </div>
            </details>
            <details className={styles.detail}>
              <summary>{copy.crystal.title}</summary>
              <div className={styles.detailBody}><p>{copy.crystal.subtitle}</p><p>{copy.crystal.note}</p><h3>{copy.crystal.carriedTitle}</h3><ul>{copy.crystal.carried.map((item) => <li key={item}>{item}</li>)}</ul></div>
            </details>
            <details className={styles.detail}>
              <summary>{isRu ? "Посмотреть пример работы с Box" : "See an example Box workflow"}</summary>
              <div className={styles.disclosureDemo}><BoxDemo title={copy.demo.title} subtitle={copy.demo.subtitle} recordingLabel={copy.demo.recordingLabel} playLabel={copy.demo.playLabel} replayLabel={copy.demo.replayLabel} lines={demoLines} /></div>
            </details>
          </div>
        </div>
      </section>

      <div className={styles.embedded}><BoxAccessForm copy={copy} locale={locale} /></div>
      <div className={styles.wrap}>
        <section className={styles.callout}>
          <div><h2 className={styles.heading}>{isRu ? "Начните с одной задачи" : "Start with one task"}</h2><p>{copy.heroNote}</p></div>
          <Link href={connectHref} data-ux="box_connect:closing_cta" className={styles.primary}>{copy.heroPrimary}<ArrowRight size={17} aria-hidden="true" /></Link>
        </section>
      </div>
    </div>
  );
}
