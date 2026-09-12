"use client";

import Link from "next/link";
import { useLang } from "@/lib/i18n/context";
import { localeHref } from "@/lib/site";
import {
  ResourceShell, ResourceHero, ResourceBenefits, ResourceSteps, ResourceDetails,
  ResourceFaq, ResourceEnd, resourceStyles as styles,
} from "@/components/marketing/resource-landing";

export default function StoragePage() {
  const { t, locale } = useLang();
  const en = locale === "en";
  const benefits = en ? [
    { title: "Use the tools you know", desc: "Connect aws-cli, rclone, boto3 or another S3 client. Change the endpoint and keys, keep your upload code." },
    { title: "Files outlive the deployment", desc: "Keep uploads and backups outside the container. Rebuilding the app does not remove objects from the bucket." },
    { title: "Choose who can open a file", desc: "Use direct URLs for public assets and temporary signed links for private documents." },
  ] : [
    { title: "Привычные инструменты", desc: "Подключите aws-cli, rclone, boto3 или другой S3-клиент. Замените endpoint и ключи, сохраните код загрузки файлов." },
    { title: "Файлы переживут деплой", desc: "Храните загрузки и бэкапы вне контейнера. Пересборка приложения не удалит объекты из бакета." },
    { title: "Доступ — по вашей задаче", desc: "Прямые ссылки для публичных файлов, временные подписанные ссылки — для личных документов." },
  ];
  const steps = en ? [
    { title: "Create a bucket", desc: "Choose a name, project and public or private access. Data is stored on servers in Russia." },
    { title: "Connect your S3 client", desc: "Get the endpoint and access keys in the console. Pass keys to your app as environment variables, not repository files." },
    { title: "Upload and share", desc: "Use an S3-compatible SDK or CLI to upload files. Share public URLs or signed links with limited lifetimes." },
  ] : [
    { title: "Создайте бакет", desc: "Выберите имя, проект и режим доступа: публичный или приватный. Данные будут храниться на серверах в России." },
    { title: "Подключите S3-клиент", desc: "Получите endpoint и ключи доступа в консоли. Передайте ключи приложению через переменные окружения, не добавляя их в репозиторий." },
    { title: "Загружайте и делитесь", desc: "Отправляйте файлы через S3-совместимый SDK или CLI. Используйте публичные URL или подписанные ссылки с ограниченным сроком действия." },
  ];
  return <ResourceShell>
    <ResourceHero kind="storage" eyebrow="S3 STORAGE · BETA"
      title={en ? ["A home for files.", "Outside your app."] : ["Место для файлов.", "Отдельно от кода."]}
      description={en ? "S3-compatible storage for user uploads, media and backups. Your app can restart and update while files stay in their bucket." : "S3-совместимое хранилище для загрузок пользователей, медиа и бэкапов. Приложение обновляется и перезапускается, а файлы остаются в бакете."}
      cta={en ? "Create a bucket" : "Создать бакет"}
      note={en ? "Beta. Keep another copy of critical data. Storage space and traffic are metered." : "Бета-версия. Для критичных данных держите ещё одну копию. Оплачиваются занятый объём и трафик."} />
    <ResourceBenefits items={benefits} />
    <ResourceSteps path="/storage" title={en ? "From bucket to first upload." : "От бакета — к первому файлу."} steps={steps} />
    <ResourceDetails title={en ? "Know the limits" : "Учитывайте ограничения"}
      intro={en ? "S3 buckets are available in Beta. There is no built-in CDN, object versioning or fine-grained IAM policy support yet." : "S3-бакеты доступны в бета-версии. Встроенного CDN, версионирования объектов и детальных IAM-политик пока нет."}
      items={t.storage.limits} />
    <ResourceFaq path="/storage" title={t.storage.faqTitle} items={t.storage.faq} />
    <ResourceEnd title={en ? "Give your files their own space." : "Выделите файлам своё место."} cta={en ? "Create a bucket" : "Создать бакет"} guide="/developer/object-storage">
      <nav className={styles.related} aria-label={en ? "Related products" : "Другие продукты"}><Link href={localeHref("/databases", locale)}>PostgreSQL</Link><Link href={localeHref("/cloud-servers", locale)}>App Servers</Link><Link href={localeHref("/pricing", locale)}>{t.nav.pricing}</Link></nav>
    </ResourceEnd>
  </ResourceShell>;
}
