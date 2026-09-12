"use client";

import Link from "next/link";
import { ArrowRight, Server, Plus } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { localeHref } from "@/lib/site";
import {
  ResourceShell, ResourceHero, ResourceBenefits, ResourceSteps, ResourceDetails,
  ResourceFaq, ResourceEnd, ResourceCta, resourceStyles as styles,
} from "@/components/marketing/resource-landing";

export default function CloudServersPage() {
  const { t, locale } = useLang();
  const en = locale === "en";
  const steps = en ? [
    { title: "Add your server", desc: "Connect a machine over SSH or order a new VM. The platform installs Docker and an edge agent. The SSH key is used once and is not stored." },
    { title: "Choose what to deploy", desc: "Connect a Git repository, use your Docker Compose file, or discover existing containers. Discovery is read-only until you choose to import a service." },
    { title: "Manage it from the console", desc: "Deploy updates, read container logs and check CPU, memory and disk usage. Applications stay on your server." },
  ] : [
    { title: "Добавьте сервер", desc: "Подключите машину по SSH или закажите новую VM. Платформа установит Docker и агента. SSH-ключ используется один раз и не сохраняется." },
    { title: "Выберите приложение", desc: "Подключите Git-репозиторий, используйте свой Docker Compose или найдите уже работающие контейнеры. До импорта поиск ничего в них не меняет." },
    { title: "Управляйте из панели", desc: "Выпускайте обновления, читайте логи контейнеров и следите за CPU, памятью и диском. Приложения остаются на вашем сервере." },
  ];
  const benefits = en ? [
    { title: "Keep what already works", desc: "Discover running containers and import them as managed apps. Their data volumes are preserved." },
    { title: "Deploy your way", desc: "Use Git or your existing docker-compose.yml. No new configuration format to learn." },
    { title: "One view of every server", desc: "Deployment, logs and metrics for your own machines and client VMs, in the same console." },
  ] : [
    { title: "Сохраните то, что работает", desc: "Найдите запущенные контейнеры и добавьте их как управляемые приложения. Тома с данными сохранятся." },
    { title: "Выпускайте обновления привычно", desc: "Из Git или своего docker-compose.yml. Новый формат конфигурации осваивать не нужно." },
    { title: "Соберите серверы в одной панели", desc: "Деплои, логи и метрики ваших машин и клиентских VM — без переключения между SSH-сессиями." },
  ];
  return <ResourceShell>
    <ResourceHero kind="servers" eyebrow="APP SERVERS" title={en ? ["Your server.", "Your control."] : ["Свой сервер.", "Под вашим контролем."]}
      description={en ? "Bring your VPS or get a new VM. Deploy apps, read logs and monitor resources from DADA Cloud — while your code runs on your machine." : "Подключите свой VPS или закажите новую VM. Деплои, логи и метрики будут в DADA Cloud, а приложения — на вашем сервере."}
      cta={en ? "Connect a server" : "Подключить сервер"}
      note={en ? "Start in the console. Choose an existing server or a new VM there." : "Начнём в консоли. Там можно выбрать свой сервер или новую VM."} />
    <ResourceBenefits items={benefits} />

    <section className={`${styles.container} ${styles.section}`}>
      <div className={styles.sectionHeading}><p className={styles.eyebrow}>{en ? "TWO WAYS TO START" : "ДВА СПОСОБА НАЧАТЬ"}</p><h2>{en ? "Already have a server?" : "Сервер уже есть?"}</h2></div>
      <div className={styles.choices}>
        <article><Server size={26} strokeWidth={1.5} aria-hidden="true" /><h3>{en ? "Connect your own" : "Подключите свой"}</h3><p>{en ? "Keep the machine at your current provider. We install the agent over SSH and show your existing containers. You decide which ones to manage." : "Оставьте машину у своего хостера. Мы установим агента по SSH и покажем существующие контейнеры. Какие из них передать под управление — решаете вы."}</p><Link className={styles.textLink} href={localeHref("/developer/app-servers-bring-your-own-vm", locale)}>{en ? "Connection guide" : "Как подключить свой сервер"}<ArrowRight size={17} aria-hidden="true" /></Link></article>
        <article><Plus size={26} strokeWidth={1.5} aria-hidden="true" /><h3>{en ? "Start with a new VM" : "Начните с новой VM"}</h3><p>{en ? "Choose a configuration, region and OS image in the console. We provision the machine; you deploy applications and manage them alongside your other servers." : "Выберите конфигурацию, регион и образ ОС в консоли. Мы создадим машину, а вы сможете запускать приложения и управлять ими рядом с остальными серверами."}</p><div className={styles.actions}><ResourceCta placement="server_choice">{en ? "Choose a VM" : "Выбрать VM"}</ResourceCta></div></article>
      </div>
    </section>

    <ResourceSteps path="/cloud-servers" title={en ? "From server to running app." : "От сервера — к приложению."} steps={steps} />
    <ResourceDetails title={en ? "What stays on your side" : "Что остаётся на вашей стороне"}
      intro={en ? "App Servers manage containers. Hardware, capacity and availability of your own machine remain your responsibility." : "App Servers управляет контейнерами. За железо, запас ресурсов и доступность своей машины отвечаете вы."}
      items={t.servers.limits} />
    <ResourceFaq path="/cloud-servers" title={t.servers.faqTitle} items={t.servers.faq} />
    <ResourceEnd title={en ? "Give your server a control panel." : "Добавьте серверу удобное управление."} cta={en ? "Connect a server" : "Подключить сервер"} guide="/developer/app-servers-bring-your-own-vm">
      <nav className={styles.related} aria-label={en ? "Related products" : "Другие продукты"}><Link href={localeHref("/databases", locale)}>PostgreSQL</Link><Link href={localeHref("/storage", locale)}>{en ? "S3 storage" : "S3-хранилище"}</Link><Link href={localeHref("/pricing", locale)}>{t.nav.pricing}</Link></nav>
    </ResourceEnd>
  </ResourceShell>;
}
