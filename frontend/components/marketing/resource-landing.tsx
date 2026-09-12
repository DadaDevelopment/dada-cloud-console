"use client";

import type { ReactNode } from "react";
import Link from "next/link";
import { ArrowRight, Database, Server, Box, Folder, Cloud, ChevronDown } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { consoleHref, localeHref } from "@/lib/site";
import { GOAL_LANDING_CTA, reachGoal } from "@/lib/metrika";
import { FaqJsonLd } from "./faq-jsonld";
import { HowToJsonLd } from "./howto-jsonld";
import styles from "./resource-landing.module.css";

export { styles as resourceStyles };
export type ResourceKind = "servers" | "databases" | "storage";
export type ResourceStep = { title: string; desc: string };

export function ResourceShell({ children }: { children: ReactNode }) {
  return <div className={styles.page}>{children}</div>;
}

export function ResourceCta({ children, placement = "hero" }: { children: ReactNode; placement?: string }) {
  return <Link href={consoleHref("/login")} className={styles.button} onClick={() => reachGoal(GOAL_LANDING_CTA, { source: "direct", placement })}>{children}<ArrowRight size={18} aria-hidden="true" /></Link>;
}

function ResourceDiagram({ kind }: { kind: ResourceKind }) {
  const { locale } = useLang();
  const en = locale === "en";
  const labels = {
    servers: { top: "DADA Cloud", middle: en ? "Your server" : "Ваш сервер", bottom: en ? ["Website", "API", "Worker"] : ["Сайт", "API", "Воркер"], foot: en ? "Apps stay on your machine." : "Приложения остаются на вашей машине." },
    databases: { top: en ? "Your application" : "Ваше приложение", middle: "PostgreSQL", bottom: en ? ["Backups", "Metrics", "Access"] : ["Бэкапы", "Метрики", "Доступ"], foot: en ? "Connected through DATABASE_URL." : "Подключение через DATABASE_URL." },
    storage: { top: en ? "Your application" : "Ваше приложение", middle: en ? "S3 bucket" : "S3-бакет", bottom: en ? ["Images", "Documents", "Backups"] : ["Фото", "Документы", "Бэкапы"], foot: en ? "Files live independently of the app." : "Файлы хранятся отдельно от приложения." },
  }[kind];
  const Icon = kind === "servers" ? Server : kind === "databases" ? Database : Folder;
  return <figure className={`${styles.diagram} ${styles[kind]}`} aria-label={labels.foot}>
    <div className={styles.diagramCaption}><span>{en ? "HOW IT FITS TOGETHER" : "КАК ЭТО УСТРОЕНО"}</span><span aria-hidden="true">↗</span></div>
    <div className={styles.diagramFlow} aria-hidden="true">
      <div className={styles.diagramSource}>{kind === "servers" ? <Cloud size={19} /> : <Box size={19} />}{labels.top}</div>
      <div className={styles.connector}><span>{kind === "servers" ? (en ? "management" : "управление") : kind === "databases" ? "DATABASE_URL" : "S3 API"}</span></div>
      <div className={styles.diagramResource}><Icon size={40} strokeWidth={1.3} /><strong>{labels.middle}</strong><div className={styles.diagramChips}>{labels.bottom.map(label => <span key={label}>{label}</span>)}</div></div>
    </div>
    <figcaption>{labels.foot}</figcaption>
  </figure>;
}

export function ResourceHero({ kind, eyebrow, title, description, cta, note }: { kind: ResourceKind; eyebrow: string; title: [string, string]; description: string; cta: string; note?: string }) {
  const { locale } = useLang();
  return <section className={`${styles.container} ${styles.hero}`}>
    <div className={styles.heroCopy}><p className={styles.eyebrow}>{eyebrow}</p><h1>{title[0]}<span>{title[1]}</span></h1><p className={styles.lead}>{description}</p><div className={styles.actions}><ResourceCta>{cta}</ResourceCta><a className={styles.textLink} href="#how">{locale === "en" ? "How it works" : "Как это работает"}<span aria-hidden="true">↓</span></a></div>{note && <p className={styles.note}>{note}</p>}</div>
    <ResourceDiagram kind={kind} />
  </section>;
}

export function ResourceBenefits({ items }: { items: ResourceStep[] }) {
  return <div className={`${styles.container} ${styles.benefits}`}>{items.map((item, index) => <article key={item.title}><span className={styles.number}>0{index + 1}</span><h2>{item.title}</h2><p>{item.desc}</p></article>)}</div>;
}

export function ResourceSteps({ path, title, steps }: { path: string; title: string; steps: ResourceStep[] }) {
  const { locale } = useLang();
  return <section id="how" className={`${styles.container} ${styles.section}`}>
    <HowToJsonLd path={path} name={title} description={title} steps={steps.map(item => ({ name: item.title, text: item.desc }))} />
    <div className={styles.sectionHeading}><p className={styles.eyebrow}>{locale === "en" ? "GET STARTED" : "НАЧАТЬ РАБОТУ"}</p><h2>{title}</h2></div>
    <ol className={styles.steps}>{steps.map((step, index) => <li key={step.title} id={`step-${String(index + 1).padStart(2, "0")}`}><span className={styles.stepNumber}>0{index + 1}</span><h3>{step.title}</h3><p>{step.desc}</p></li>)}</ol>
  </section>;
}

export function ResourceDetails({ title, intro, items }: { title: string; intro?: string; items: ResourceStep[] }) {
  return <section className={`${styles.container} ${styles.detailsLayout}`}><div><h2>{title}</h2>{intro && <p>{intro}</p>}</div><div className={styles.disclosures}>{items.map(item => <details key={item.title}><summary>{item.title}<ChevronDown size={18} aria-hidden="true" /></summary><p>{item.desc}</p></details>)}</div></section>;
}

export function ResourceFaq({ path, title, items }: { path: string; title: string; items: { q: string; a: string }[] }) {
  const { locale } = useLang();
  return <section className={`${styles.container} ${styles.detailsLayout}`}><FaqJsonLd path={path} items={items} /><h2>{title}</h2><div className={styles.disclosures}>{items.slice(0, 4).map(item => <details key={item.q}><summary>{item.q}<ChevronDown size={18} aria-hidden="true" /></summary><p>{item.a}</p></details>)}{items.length > 4 && <details className={styles.moreQuestions}><summary>{locale === "en" ? "More questions" : "Ещё вопросы"}<ChevronDown size={18} aria-hidden="true" /></summary><div>{items.slice(4).map(item => <details key={item.q}><summary>{item.q}<ChevronDown size={18} aria-hidden="true" /></summary><p>{item.a}</p></details>)}</div></details>}</div></section>;
}

export function ResourceEnd({ title, cta, guide, children }: { title: string; cta: string; guide: string; children?: ReactNode }) {
  const { locale } = useLang();
  return <section className={styles.end}><div className={`${styles.container} ${styles.endInner}`}><h2>{title}</h2><div className={styles.actions}><ResourceCta placement="band">{cta}</ResourceCta><Link className={styles.textLink} href={localeHref(guide, locale)}>{locale === "en" ? "Read the guide" : "Открыть руководство"}<ArrowRight size={17} aria-hidden="true" /></Link></div>{children}</div></section>;
}
