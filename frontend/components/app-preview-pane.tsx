"use client";

import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n/console/context";
import { Spinner } from "@/components/ui/spinner";
import { ChevronDown, ExternalLink, RefreshCw, Smartphone, Tablet, Maximize2 } from "lucide-react";

type Viewport = "mobile" | "tablet" | "full";

const VIEWPORT_WIDTH: Record<Viewport, string> = {
  mobile: "375px",
  tablet: "768px",
  full: "100%",
};

interface AppPreviewPaneProps {
  url: string;
  title?: string;
  defaultOpen?: boolean;
}

/**
 * Claude-Code-style embedded live preview: a sandboxed iframe for a deployed
 * app's URL, gated by a server-side frame-check (X-Frame-Options / CSP
 * frame-ancestors) so a blocking app shows a fallback instead of a dead frame.
 */
export function AppPreviewPane({ url, title, defaultOpen = false }: AppPreviewPaneProps) {
  const { t } = useT();
  const [open, setOpen] = useState(defaultOpen);
  const [viewport, setViewport] = useState<Viewport>("full");
  const [reloadKey, setReloadKey] = useState(0);
  const [embeddable, setEmbeddable] = useState<boolean | null>(null);
  const checking = open && embeddable === null;

  function handleReload() {
    setEmbeddable(null);
    setReloadKey((k) => k + 1);
  }

  useEffect(() => {
    if (!open || embeddable !== null) return;
    let cancelled = false;
    fetch(`/api/frame-check?url=${encodeURIComponent(url)}`)
      .then((res) => res.json())
      .then((data: { embeddable?: boolean }) => {
        if (cancelled) return;
        setEmbeddable(data.embeddable ?? false);
      })
      .catch(() => {
        if (!cancelled) setEmbeddable(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, url, embeddable]);

  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 shadow-sm">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between gap-3 px-5 py-4"
      >
        <span className="text-sm font-semibold text-gray-900 dark:text-gray-100">
          {title ?? t("previewPane.title")}
        </span>
        <ChevronDown
          className={`h-4 w-4 shrink-0 text-gray-400 dark:text-gray-500 transition-transform ${open ? "rotate-180" : ""}`}
        />
      </button>

      {open && (
        <div className="border-t border-gray-100 dark:border-gray-800 p-4">
          <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-1 rounded-lg border border-gray-200 dark:border-gray-800 p-0.5">
              <button
                type="button"
                onClick={() => setViewport("mobile")}
                title={t("previewPane.viewport.mobile")}
                className={`flex h-7 w-7 items-center justify-center rounded-md transition-colors ${
                  viewport === "mobile"
                    ? "bg-blue-50 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400"
                    : "text-gray-400 dark:text-gray-500 hover:text-gray-600 dark:hover:text-gray-300"
                }`}
              >
                <Smartphone className="h-4 w-4" />
              </button>
              <button
                type="button"
                onClick={() => setViewport("tablet")}
                title={t("previewPane.viewport.tablet")}
                className={`flex h-7 w-7 items-center justify-center rounded-md transition-colors ${
                  viewport === "tablet"
                    ? "bg-blue-50 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400"
                    : "text-gray-400 dark:text-gray-500 hover:text-gray-600 dark:hover:text-gray-300"
                }`}
              >
                <Tablet className="h-4 w-4" />
              </button>
              <button
                type="button"
                onClick={() => setViewport("full")}
                title={t("previewPane.viewport.full")}
                className={`flex h-7 w-7 items-center justify-center rounded-md transition-colors ${
                  viewport === "full"
                    ? "bg-blue-50 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400"
                    : "text-gray-400 dark:text-gray-500 hover:text-gray-600 dark:hover:text-gray-300"
                }`}
              >
                <Maximize2 className="h-4 w-4" />
              </button>
            </div>

            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={handleReload}
                className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 dark:border-gray-800 px-2.5 py-1.5 text-xs font-medium text-gray-600 dark:text-gray-300 hover:border-blue-300 hover:text-blue-600 transition-colors"
              >
                <RefreshCw className="h-3.5 w-3.5" />
                {t("previewPane.reload")}
              </button>
              <a
                href={url}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 dark:border-gray-800 px-2.5 py-1.5 text-xs font-medium text-gray-600 dark:text-gray-300 hover:border-blue-300 hover:text-blue-600 transition-colors"
              >
                <ExternalLink className="h-3.5 w-3.5" />
                {t("previewPane.openNewTab")}
              </a>
            </div>
          </div>

          {checking ? (
            <div className="flex h-64 items-center justify-center rounded-lg border border-gray-100 dark:border-gray-800 bg-gray-50 dark:bg-gray-950">
              <Spinner />
              <span className="ml-2 text-sm text-gray-400 dark:text-gray-500">{t("previewPane.checking")}</span>
            </div>
          ) : embeddable === false ? (
            <div className="flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-950 py-12 text-center">
              <p className="text-sm font-medium text-gray-700 dark:text-gray-200">{t("previewPane.blocked.title")}</p>
              <p className="max-w-sm text-xs text-gray-400 dark:text-gray-500">{t("previewPane.blocked.body")}</p>
              <a
                href={url}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1.5 text-sm font-medium text-blue-600 dark:text-blue-400 hover:underline"
              >
                <ExternalLink className="h-4 w-4" />
                {t("previewPane.openNewTab")}
              </a>
            </div>
          ) : (
            <div className="flex justify-center overflow-x-auto rounded-lg border border-gray-100 dark:border-gray-800 bg-gray-50 dark:bg-gray-950 p-3">
              <iframe
                key={reloadKey}
                src={url}
                sandbox="allow-scripts allow-same-origin allow-forms"
                referrerPolicy="no-referrer"
                style={{ width: VIEWPORT_WIDTH[viewport], height: "600px", maxWidth: "100%" }}
                className="rounded-md border border-gray-200 dark:border-gray-800 bg-white"
                title={title ?? t("previewPane.title")}
              />
            </div>
          )}
        </div>
      )}
    </div>
  );
}
