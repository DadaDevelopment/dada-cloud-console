"use client";

import Link from "next/link";
import { useLang } from "@/lib/i18n/context";
import { localeHref } from "@/lib/site";
import {
  ResourceShell, ResourceHero, ResourceBenefits, ResourceSteps, ResourceDetails,
  ResourceFaq, ResourceEnd, resourceStyles as styles,
} from "@/components/marketing/resource-landing";

export default function DatabasesPage() {
  const { t, locale } = useLang();
  const en = locale === "en";
  const benefits = en ? [
    { title: "Your connection is ready", desc: "Bind the database to an app. DATABASE_URL is added to its environment automatically." },
    { title: "Backups you can take with you", desc: "Choose an hourly or daily schedule and 7, 14 or 30 days of retention. Download copies from the console." },
    { title: "Room to grow", desc: "Change instance resources without moving data manually. Your app keeps the same connection string." },
  ] : [
    { title: "Подключение уже готово", desc: "Привяжите базу к приложению — DATABASE_URL появится в его переменных окружения автоматически." },
    { title: "Бэкапы можно забрать с собой", desc: "Выберите копирование каждый час или день и хранение 7, 14 или 30 дней. Скачивайте копии из консоли." },
    { title: "Есть куда расти", desc: "Меняйте ресурсы инстанса без ручного переноса данных. Приложение продолжит использовать ту же строку подключения." },
  ];
  const steps = en ? [
    { title: "Create PostgreSQL", desc: "Choose a project, instance size and disk space. The database runs on the platform's infrastructure in Russia." },
    { title: "Set up backups", desc: "Select a schedule and retention period at creation time. Copies are stored separately, in object storage." },
    { title: "Connect your application", desc: "Select the database in your app settings. DATABASE_URL arrives automatically; connections and resource usage are visible in the console." },
  ] : [
    { title: "Создайте PostgreSQL", desc: "Выберите проект, размер инстанса и объём диска. База развернётся на инфраструктуре платформы в России." },
    { title: "Настройте бэкапы", desc: "При создании выберите расписание и срок хранения. Копии будут храниться отдельно от базы — в объектном хранилище." },
    { title: "Подключите приложение", desc: "Выберите базу в настройках приложения. DATABASE_URL добавится автоматически, а подключения и нагрузка будут видны в консоли." },
  ];
  return <ResourceShell>
    <ResourceHero kind="databases" eyebrow={en ? "MANAGED POSTGRESQL" : "УПРАВЛЯЕМЫЙ POSTGRESQL"}
      title={en ? ["Write your app.", "We'll run the database."] : ["Пишите приложение.", "Базу настроим мы."]}
      description={en ? "PostgreSQL next to your application. Automatic connection, scheduled backups and monitoring — without installing and maintaining the database yourself." : "PostgreSQL рядом с вашим приложением. Подключение, бэкапы по расписанию и мониторинг — без самостоятельной установки и сопровождения базы."}
      cta={en ? "Create a database" : "Создать базу данных"}
      note={en ? "PostgreSQL is the managed engine. Other databases run in containers on App Servers." : "Управляемый движок — PostgreSQL. Другие базы можно запустить контейнерами на App Servers."} />
    <ResourceBenefits items={benefits} />
    <ResourceSteps path="/databases" title={en ? "Ready for your first query." : "Всё готово для первого запроса."} steps={steps} />
    <ResourceDetails title={en ? "You own the data. We handle operations." : "Ваши данные. Наша забота о базе."}
      intro={en ? "Backups, schema restore, monitoring and access in the same project as your app." : "Бэкапы, восстановление схемы, мониторинг и доступ — в том же проекте, что и приложение."}
      items={t.databases.features.filter((_, index) => [1, 2, 3].includes(index))} />
    <ResourceDetails title={en ? "Before you migrate" : "Перед переносом базы"} items={t.databases.limits} />
    <ResourceFaq path="/databases" title={t.databases.faqTitle} items={t.databases.faq} />
    <ResourceEnd title={en ? "Connect your app to PostgreSQL." : "Подключите приложение к PostgreSQL."} cta={en ? "Create a database" : "Создать базу данных"} guide="/developer/databases-postgres">
      <nav className={styles.related} aria-label={en ? "Related products" : "Другие продукты"}><Link href={localeHref("/cloud-servers", locale)}>{en ? "MySQL or Redis on your server" : "MySQL или Redis на своём сервере"}</Link><Link href={localeHref("/storage", locale)}>{en ? "S3 storage" : "S3-хранилище"}</Link><Link href={localeHref("/pricing", locale)}>{t.nav.pricing}</Link></nav>
    </ResourceEnd>
  </ResourceShell>;
}
