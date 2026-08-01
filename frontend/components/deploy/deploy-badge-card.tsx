"use client";

import { useT } from "@/lib/i18n/console/context";
import { CopyButton } from "@/components/ui/copy-button";
import {
  deployBadgeHtml,
  deployBadgeImage,
  deployBadgeLink,
  deployBadgeMarkdown,
} from "@/lib/deploy-badge";

/**
 * "Deploy on Dada" badge card on the app detail page. Shown once an app has a
 * linked git repo: it hands the repo owner a ready-made README snippet whose
 * badge points at `/deploy?repo=<owner>/<name>`, the same one-click landing the
 * Railway/Render buttons use.
 *
 * The console origin is read from the browser rather than configuration so the
 * copied snippet always references the host the user is actually on.
 */
export function DeployBadgeCard({ repoFullName }: { repoFullName: string }) {
  const { t } = useT();
  const baseUrl = typeof window === "undefined" ? "" : window.location.origin;
  const markdown = deployBadgeMarkdown(baseUrl, repoFullName);
  const html = deployBadgeHtml(baseUrl, repoFullName);

  return (
    <section>
      <div className="mb-4">
        <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
          {t("deployBadge.title")}
        </h2>
        <p className="text-sm text-gray-400 dark:text-gray-500">{t("deployBadge.subtitle")}</p>
      </div>

      <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
        <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
          {t("deployBadge.preview")}
        </p>
        <a
          href={deployBadgeLink(baseUrl, repoFullName)}
          target="_blank"
          rel="noreferrer noopener"
          className="mt-2 inline-block"
        >
          <img src={deployBadgeImage(baseUrl)} alt="Deploy on Dada" height={40} width={196} />
        </a>

        <div className="mt-5">
          <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
            {t("deployBadge.markdown")}
          </p>
          <div className="relative mt-1.5">
            <pre className="overflow-x-auto rounded-lg border border-gray-800 bg-gray-900 p-3 pr-20 font-mono text-xs text-gray-100">
              {markdown}
            </pre>
            <div className="absolute right-2 top-2">
              <CopyButton value={markdown} label={t("common.copy")} />
            </div>
          </div>
        </div>

        <div className="mt-4">
          <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
            {t("deployBadge.html")}
          </p>
          <div className="relative mt-1.5">
            <pre className="overflow-x-auto rounded-lg border border-gray-800 bg-gray-900 p-3 pr-20 font-mono text-xs text-gray-100">
              {html}
            </pre>
            <div className="absolute right-2 top-2">
              <CopyButton value={html} label={t("common.copy")} />
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
