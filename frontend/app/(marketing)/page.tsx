"use client";

import Link from "next/link";
import { ArrowRight, ArrowUpRight, Box, Check, GitBranch, Server, Database, HardDrive, Sparkles } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { consoleHref, localeHref } from "@/lib/site";
import { FaqList } from "@/components/marketing/sections";
import { GOAL_LANDING_CTA, reachGoal } from "@/lib/metrika";
import { HomeJsonLd } from "@/components/marketing/home-jsonld";
import { DeployPreview } from "@/components/marketing/deploy-preview";
import { LaunchScene } from "@/components/marketing/launch-scene";
import styles from "./home.module.css";

export default function HomePage() {
  const { t, locale } = useLang();
  const en = locale === "en";
  const start = (placement: string) => reachGoal(GOAL_LANDING_CTA, { source: "direct", placement });
  const choices = [
    { icon: GitBranch, label: en ? "01 / APPLICATIONS" : "01 / ПРИЛОЖЕНИЯ", title: en ? "I have code.\nI want it online." : "Есть код.\nНужен запуск.", text: en ? "Connect GitHub. Get builds, a public address and logs in one place." : "Подключите GitHub. Получите сборку, адрес приложения и логи в одном месте.", action: en ? "Deploy an app" : "Запустить приложение", href: consoleHref("/login"), goal: true },
    { icon: Server, label: en ? "02 / SERVERS" : "02 / СЕРВЕРЫ", title: en ? "My server.\nLess manual work." : "Свой сервер.\nМеньше ручной работы.", text: en ? "Bring your VPS or order a VM. Manage deployments and containers from one console." : "Подключите VPS или закажите VM. Управляйте деплоями и контейнерами из консоли.", action: en ? "Choose a server" : "Выбрать вариант", href: localeHref("/cloud-servers", locale) },
    { icon: Box, label: en ? "03 / AI AGENTS" : "03 / AI-АГЕНТЫ", title: en ? "An agent needs\nroom to work." : "Агенту нужно\nместо для работы.", text: en ? "Give Claude, Cursor or Codex a cloud computer with tools and root access." : "Дайте Claude, Cursor или Codex облачный компьютер с инструментами и root-доступом.", action: en ? "Explore Dada Box" : "Посмотреть Dada Box", href: localeHref("/box", locale) },
  ];
  return <div className={styles.home}>
    <HomeJsonLd />
    <section className={styles.hero}>
      <div className={`${styles.container} ${styles.heroGrid}`}>
        <div className={styles.heroCopy}>
          <p className={styles.eyebrow}><span />{en ? "DADA CLOUD / BUILT FOR YOUR NEXT PROJECT" : "DADA CLOUD / ДЛЯ ВАШЕГО СЛЕДУЮЩЕГО ПРОЕКТА"}</p>
          <h1>{en ? "Your code." : "Ваш код."}<br/><em>{en ? "Our cloud." : "Наше облако."}</em></h1>
          <p className={styles.lead}>{en ? "Launch apps from GitHub. Connect databases and domains. Build your product while we handle builds and deployment." : "Запускайте приложения из GitHub. Подключайте базы и домены. Вы создаёте продукт — облако собирает и запускает его."}</p>
          <div className={styles.actions}><Link href={consoleHref("/login")} onClick={()=>start("hero")} className={styles.primary}>{en ? "Start for free" : "Начать бесплатно"}<ArrowUpRight size={20} aria-hidden="true" /></Link><Link href="#start" className={styles.textLink}>{en ? "Find my starting point" : "Выбрать свой сценарий"}<ArrowRight size={18} aria-hidden="true" /></Link></div>
          <p className={styles.heroNote}>{en ? "Free plan: 1 app + 1 database. From 0 ₽." : "На Free: 1 приложение и 1 база. От 0 ₽."}</p>
        </div>
        <LaunchScene en={en} />
      </div>
      <div className={`${styles.container} ${styles.proofline}`}><span>{en ? "Familiar tools. One workspace." : "Знакомые инструменты. Одно рабочее место."}</span><span>GitHub</span><span>Docker</span><span>PostgreSQL</span><span>HTTPS</span><Link href={localeHref("/mcp",locale)}>MCP <ArrowUpRight size={13} aria-hidden="true" /></Link></div>
    </section>

    <section id="start" className={styles.section}><div className={styles.container}>
      <div className={styles.headingRow}><h2>{en ? "Where do\nyou want to start?" : "С чего\nначнём?"}</h2><p>{en ? "One platform. Different ways to get your work online." : "Одна платформа. Несколько способов запустить то, что вы задумали."}</p></div>
      <div className={styles.choices}>{choices.map(({icon:Icon,...choice})=><Link key={choice.label} href={choice.href} className={styles.choice} onClick={()=>choice.goal&&start("scenario_app")}><div className={styles.choiceTop}><span>{choice.label}</span><Icon size={26} strokeWidth={1.4} aria-hidden="true" /></div><h3>{choice.title}</h3><p>{choice.text}</p><span className={styles.choiceAction}>{choice.action}<ArrowUpRight size={20} aria-hidden="true" /></span></Link>)}</div>
    </div></section>

    <section id="how" className={`${styles.section} ${styles.how}`}><div className={`${styles.container} ${styles.howGrid}`}>
      <div><p className={styles.kicker}>{en ? "FROM REPOSITORY TO A PUBLIC URL" : "ОТ РЕПОЗИТОРИЯ ДО ССЫЛКИ"}</p><h2>{en ? "Push your code.\nSee it live." : "Написали.\nЗапушили.\nРаботает."}</h2><ol className={styles.steps}>{t.home.steps.map(step=><li key={step.num}><span>{step.num}</span><div><h3>{step.title}</h3><p>{step.desc}</p></div></li>)}</ol></div>
      <div className={styles.workspace}><DeployPreview /><Link href={localeHref("/developer",locale)} className={styles.textLink}>{en ? "Read the deployment guide" : "Посмотреть инструкцию запуска"}<ArrowRight size={18} aria-hidden="true" /></Link></div>
    </div></section>

    <section className={styles.section}><div className={styles.container}>
      <div className={styles.headingRow}><h2>{en ? "Your app grows.\nIts workspace does too." : "Приложение растёт.\nВсё нужное — рядом."}</h2><p>{en ? "Add resources when you need them. Keep managing everything in the same project." : "Добавляйте ресурсы по мере необходимости. Управляйте ими в том же проекте."}</p></div>
      <div className={styles.resources}>
        <Link href={localeHref("/databases",locale)}><Database size={36} strokeWidth={1.3} aria-hidden="true" /><div><h3>PostgreSQL</h3><p>{en ? "A database for your app, with backups." : "База для приложения. С резервными копиями."}</p></div><ArrowUpRight size={24} aria-hidden="true" /></Link>
        <Link href={localeHref("/storage",locale)}><HardDrive size={36} strokeWidth={1.3} aria-hidden="true" /><div><h3>{en ? "S3 storage" : "S3-хранилище"}</h3><p>{en ? "Files, images and uploads through the S3 API." : "Файлы, изображения и загрузки через S3 API."}</p></div><ArrowUpRight size={24} aria-hidden="true" /></Link>
      </div>
      <Link href={localeHref("/mcp",locale)} className={styles.mcp}><span className={styles.mcpIcon}><Sparkles size={25} aria-hidden="true" /></span><div><h3>{en ? "Prefer to ask your AI agent?" : "Удобнее попросить AI-агента?"}</h3><p>{en ? "Connect MCP. Your agent can deploy apps and inspect logs with your permissions." : "Подключите MCP: агент сможет запускать приложения и смотреть логи с вашими правами."}</p></div><span>{en ? "Connect MCP" : "Подключить MCP"}<ArrowRight size={19} aria-hidden="true" /></span></Link>
    </div></section>

    <section className={styles.pricing}><div className={`${styles.container} ${styles.pricingGrid}`}><div><p className={styles.kicker}>{en ? "START SMALL" : "НАЧНИТЕ С МАЛОГО"}</p><h2>{en ? "First app.\nZero rubles." : "Первое приложение.\nНоль рублей."}</h2><p>{en ? "Explore the platform on Free. Choose a larger plan when your project needs more." : "Попробуйте платформу на Free. Когда проекту станет тесно — выберите план побольше."}</p><Link className={styles.primary} href={consoleHref("/login")} onClick={()=>start("pricing_teaser")}>{en ? "Create a free account" : "Создать бесплатный аккаунт"}<ArrowUpRight size={20} aria-hidden="true" /></Link></div><div className={styles.priceTable}>{t.home.pricingTiers.map(tier=><div key={tier.name}><strong>{tier.name}</strong><span>{tier.price}</span><p>{tier.tagline}</p></div>)}<Link className={styles.textLink} href={localeHref("/pricing",locale)}>{en ? "Compare limits and conditions" : "Сравнить лимиты и условия"}<ArrowRight size={18} aria-hidden="true" /></Link></div></div></section>
    <FaqList title={en ? "Before you start" : "Перед стартом"} items={t.home.faq} />
    <section className={styles.finalCta}><div className={styles.container}><span className={styles.endMark} aria-hidden="true">↗</span><h2>{en ? "Make it real." : "Пора запускать."}</h2><Link href={consoleHref("/login")} onClick={()=>start("band")} className={styles.primary}>{en ? "Start for free" : "Начать бесплатно"}<ArrowUpRight size={20} aria-hidden="true" /></Link><p><Check size={15} aria-hidden="true" />{en ? "Start with one app. See how it feels." : "Начните с одного приложения. Разберётесь в процессе."}</p></div></section>
  </div>;
}
