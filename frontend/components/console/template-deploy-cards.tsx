"use client";
import { useState } from "react";
import { useRouter } from "next/navigation";
import { gitApi, buildsApi } from "@/lib/api";
import { ResourceIcon } from "@/components/shell/icons";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";
import { trackBuildStart } from "@/lib/build-watch";

type Template = { key: string; repo_full_name: string; port: number };

const TEMPLATES: Template[] = [
  { key: "nextjs", repo_full_name: "DadaDevelopment/dada-nextjs-starter", port: 3000 },
  { key: "fastapi", repo_full_name: "DadaDevelopment/dada-fastapi-starter", port: 8000 },
  { key: "static", repo_full_name: "DadaDevelopment/dada-static-starter", port: 8080 },
];

function toKubeName(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "")
    .slice(0, 63);
}

export interface TemplateDeployCardsProps {
  projectId: string;
  envId: string | null;
  /** Denser layout for secondary placements (apps empty state, git import wall). */
  compact?: boolean;
  /**
   * Prominent first-run treatment: bigger card, bigger heading, activation
   * copy. Used where this is the primary onramp (empty-project overview,
   * apps empty state) rather than a secondary option next to Git.
   */
  hero?: boolean;
  className?: string;
}

/**
 * No-GitHub escape hatch: deploys one of the starter templates by app name
 * directly, skipping the git-account connect flow entirely. Shared across the
 * project overview, the apps empty state, and the git-import OAuth wall so the
 * option is reachable everywhere a user would otherwise hit the GitHub gate.
 * `hero` swaps in the activation-focused heading copy and a larger card for
 * the placements where this is the primary onramp; the git-import wall keeps
 * the default heading since Git is already the primary action there.
 */
export function TemplateDeployCards({ projectId, envId, compact, hero, className }: TemplateDeployCardsProps) {
  const { t } = useT();
  const router = useRouter();
  const [deployingKey, setDeployingKey] = useState<string | null>(null);
  const [templateError, setTemplateError] = useState<string | null>(null);

  async function deployTemplate(tpl: Template) {
    if (!envId || deployingKey) return;
    setTemplateError(null);
    setDeployingKey(tpl.key);
    const appName = toKubeName(`${tpl.key}-${Math.random().toString(36).slice(2, 8)}`);
    try {
      try {
        await gitApi.linkRepo(projectId, envId, {
          installation_id: "",
          repo_full_name: tpl.repo_full_name,
          app_name: appName,
          production_branch: "main",
          root_dir: ".",
          auto_deploy: false,
          port: tpl.port,
          profile: "small",
        });
      } catch (err) {
        const msg = err instanceof Error ? err.message : t("overview.templates.error");
        if (!/409|already/i.test(msg)) throw new Error(msg);
      }
      const { build } = await buildsApi.trigger(projectId, envId, appName);
      if (build?.id) trackBuildStart({ projectId, envId, appName, buildId: build.id });
      router.push(`/projects/${projectId}/apps/${appName}/deployments?envId=${envId}`);
    } catch (err) {
      setTemplateError(err instanceof Error ? err.message : t("overview.templates.error"));
      setDeployingKey(null);
    }
  }

  const body = (
    <>
      {!compact && (
        <div className="mb-1">
          <h2
            className={
              hero
                ? "text-lg font-bold text-gray-900 dark:text-gray-100 sm:text-xl"
                : "text-sm font-semibold text-gray-900 dark:text-gray-100"
            }
          >
            {hero ? t("overview.templates.heroTitle") : t("overview.templates.title")}
          </h2>
          <p
            className={
              hero
                ? "mt-2 text-sm text-gray-600 dark:text-gray-300 sm:text-base"
                : "mt-1 text-sm text-gray-500 dark:text-gray-400"
            }
          >
            {hero ? t("overview.templates.heroHint") : t("overview.templates.hint")}
          </p>
        </div>
      )}
      {templateError && (
        <div className="mt-3 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-3 py-2 text-sm text-red-700 dark:text-red-300">
          {templateError}
        </div>
      )}
      <div className={`grid gap-4 sm:grid-cols-2 lg:grid-cols-3 ${compact ? "" : "mt-4"}`}>
        {TEMPLATES.map((tpl) => (
          <TemplateCard
            key={tpl.key}
            title={t(`overview.templates.${tpl.key}.title`)}
            hint={t(`overview.templates.${tpl.key}.hint`)}
            cta={deployingKey === tpl.key ? t("overview.templates.deploying") : t("overview.templates.cta")}
            busy={deployingKey === tpl.key}
            disabled={!!deployingKey || !envId}
            onClick={() => deployTemplate(tpl)}
          />
        ))}
      </div>
    </>
  );

  if (compact) {
    return <div className={className}>{body}</div>;
  }

  const containerClass = hero
    ? "rounded-2xl border-2 border-blue-200 dark:border-blue-900/60 bg-gradient-to-br from-blue-50 to-white dark:from-blue-950/20 dark:to-gray-900 p-6 shadow-sm sm:p-8"
    : "rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm";

  return (
    <div className={`${containerClass} ${className ?? ""}`}>
      {body}
    </div>
  );
}

function TemplateCard({
  title,
  hint,
  cta,
  busy,
  disabled,
  onClick,
}: {
  title: string;
  hint: string;
  cta: string;
  busy: boolean;
  disabled: boolean;
  onClick: () => void;
}) {
  return (
    <div className="flex flex-col rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
      <div className="mb-4 flex h-10 w-10 items-center justify-center rounded-lg bg-blue-100 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400">
        <ResourceIcon name="apps" className="h-5 w-5" />
      </div>
      <p className="text-sm font-semibold text-gray-900 dark:text-gray-100">{title}</p>
      <p className="mt-1 flex-1 text-sm text-gray-500 dark:text-gray-400">{hint}</p>
      <button
        type="button"
        onClick={onClick}
        disabled={disabled}
        className="mt-4 inline-flex items-center justify-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-60"
      >
        {busy && <Spinner size="sm" />}
        {cta}
      </button>
    </div>
  );
}
