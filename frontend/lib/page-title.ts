/**
 * Contextual page titles for the console.
 *
 * One generic title ("DADA Cloud Console") on every route meant a browser tab
 * with ten console pages open was ten identical tabs, and a link pasted into a
 * chat unfurled as the same nameless card no matter where it pointed. Both
 * surfaces read the same derivation so they never disagree.
 *
 * The path is the only input the server side can trust: console routes render
 * their shell unauthenticated (auth is client-side), so the crawler that fetches
 * a link preview has no session and cannot be told a project's name. Resource
 * names already live in the URL, so they are free; the project name is not, and
 * is filled in client-side once the context has loaded.
 */

import type { ConsoleLocale } from "./i18n/console/locale";

export const SITE_NAME = "DADA Cloud";

interface Label {
  ru: string;
  en: string;
}

const label = (ru: string, en: string): Label => ({ ru, en });

/** Section labels, keyed by the URL segment under /projects/[id]/. */
const SECTIONS: Record<string, Label> = {
  apps: label("Приложения", "Applications"),
  databases: label("Базы данных", "Databases"),
  storage: label("Объектное хранилище", "Object Storage"),
  domains: label("Домены", "Domains"),
  monitoring: label("Мониторинг", "Monitoring"),
  ai: label("AI API", "AI API"),
  models: label("AI-модели", "AI Models"),
  "app-servers": label("Managed VM", "Managed VM"),
  git: label("Сборки", "Builds"),
  operations: label("Операции", "Operations"),
  members: label("Участники", "Members"),
  billing: label("Биллинг", "Billing"),
};

/** Singular form used when the page is one named resource, not the list. */
const SECTION_SINGULAR: Record<string, Label> = {
  apps: label("Приложение", "Application"),
  databases: label("База данных", "Database"),
  storage: label("Бакет", "Bucket"),
  models: label("AI-модель", "AI Model"),
  "app-servers": label("Managed VM", "Managed VM"),
};

/** Tabs under /projects/[id]/apps/[appName]/. */
const APP_TABS: Record<string, Label> = {
  files: label("Файлы", "Files"),
  settings: label("Настройки", "Settings"),
  deployments: label("Деплои", "Deployments"),
  values: label("values.yaml", "values.yaml"),
  compose: label("Compose", "Compose"),
  builds: label("Сборка", "Build"),
};

const TOP_LEVEL: Record<string, Label> = {
  projects: label("Проекты", "Projects"),
  "ai-studio": label("AI Studio", "AI Studio"),
  login: label("Вход", "Sign in"),
  register: label("Регистрация", "Sign up"),
  deploy: label("Деплой репозитория", "Deploy a repository"),
  admin: label("Админка", "Admin"),
};

const ADMIN_PAGES: Record<string, Label> = {
  costs: label("Экономика", "Economics"),
  audit: label("Аудит", "Audit"),
  approvals: label("Согласования", "Approvals"),
  "ai-gateway": label("AI Gateway", "AI Gateway"),
};

const PROJECT_FALLBACK = label("Проект", "Project");

/** Title for a route the map does not know — the old site-wide default. */
const GENERIC_TITLE = `${SITE_NAME} Console`;

function pick(l: Label | undefined, locale: ConsoleLocale): string | undefined {
  return l ? l[locale] : undefined;
}

function segments(pathname: string): string[] {
  return pathname.split("?")[0].split("/").filter(Boolean);
}

/**
 * The title parts, most specific first. Callers join them; the caller is also
 * what decides whether the site name is appended.
 */
function titleParts(
  pathname: string,
  locale: ConsoleLocale,
  projectName?: string | null,
): string[] {
  const segs = segments(pathname);
  if (segs.length === 0) return [];

  if (segs[0] !== "projects" && segs[0] !== "admin") {
    const top = pick(TOP_LEVEL[segs[0]], locale);
    return top ? [top] : [];
  }

  if (segs[0] === "admin") {
    const page = pick(ADMIN_PAGES[segs[1] ?? ""], locale);
    const admin = pick(TOP_LEVEL.admin, locale)!;
    return page ? [page, admin] : [admin];
  }

  if (segs.length === 1) return [pick(TOP_LEVEL.projects, locale)!];

  const scope = projectName ? [projectName] : [];
  const section = segs[2];
  if (!section) return [projectName || pick(PROJECT_FALLBACK, locale)!];

  const sectionLabel = pick(SECTIONS[section], locale);
  const resource = segs[3];
  if (!resource || section === "monitoring" || section === "git") {
    return sectionLabel ? [sectionLabel, ...scope] : [projectName || pick(PROJECT_FALLBACK, locale)!];
  }

  const singular = pick(SECTION_SINGULAR[section], locale) ?? sectionLabel;

  if (section === "apps") {
    const tab = segs[4];
    if (tab === "builds" && segs[5]) {
      return [`${resource} · ${pick(APP_TABS.builds, locale)} ${segs[5]}`, ...scope];
    }
    const tabLabel = pick(APP_TABS[tab ?? ""], locale);
    if (tabLabel) return [`${resource} · ${tabLabel}`, ...scope];
  }

  return singular ? [`${resource} · ${singular}`, ...scope] : [resource, ...scope];
}

export interface PageMeta {
  /** Full document title, site name included. */
  title: string;
  /** One line for link unfurls; never leaks anything the URL does not. */
  description: string;
}

const GENERIC_DESCRIPTION = label(
  "GitOps-облако: приложения, базы данных и домены в одной консоли",
  "GitOps-backed self-service cloud console",
);

/**
 * Derives the document title and unfurl description for a console path.
 *
 * @param pathname URL path without the query string (a query is tolerated).
 * @param projectName resolved project name; omitted server-side, where no
 *   session exists to resolve it, and supplied by the client once loaded.
 */
export function describePath(
  pathname: string,
  locale: ConsoleLocale,
  projectName?: string | null,
): PageMeta {
  const parts = titleParts(pathname, locale, projectName);
  if (parts.length === 0) {
    return { title: GENERIC_TITLE, description: pick(GENERIC_DESCRIPTION, locale)! };
  }
  const head = parts.join(" · ");
  return {
    title: `${head} — ${SITE_NAME}`,
    description:
      locale === "ru"
        ? `${head} — консоль DADA Cloud`
        : `${head} — DADA Cloud console`,
  };
}
