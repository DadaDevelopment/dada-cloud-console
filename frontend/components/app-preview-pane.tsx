"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useT } from "@/lib/i18n/console/context";
import { Spinner } from "@/components/ui/spinner";
import type { SiteCard } from "@/lib/site-card";
import { AlertTriangle, ChevronDown, ExternalLink, Play, RefreshCw, Smartphone, Tablet, Maximize2 } from "lucide-react";

const GATEWAY_ERROR_STATUSES = new Set([502, 503, 504]);

type Viewport = "mobile" | "tablet" | "full";

const VIEWPORT_WIDTH: Record<Viewport, string> = {
  mobile: "375px",
  tablet: "768px",
  full: "100%",
};

/** Hostname of a URL, or the raw string when it is not parseable. */
function hostOf(raw: string): string {
  try {
    return new URL(raw).host;
  } catch {
    return raw;
  }
}

interface AppPreviewPaneProps {
  url: string;
  openUrl?: string;
  detailsUrl: string;
  title?: string;
  defaultOpen?: boolean;
}

/**
 * Claude-Code-style embedded live preview: a sandboxed iframe for a deployed
 * app's URL, gated by a server-side frame-check (X-Frame-Options / CSP
 * frame-ancestors) so a blocking app shows a fallback instead of a dead frame.
 *
 * `url` is the iframe source — the backend hands out a preview-gate URL
 * (*.pv.dada-tuda.ru) with frame-blocking headers scrubbed, so the check
 * passes for any app. `openUrl` is the app's real address used for the UI
 * links; it defaults to `url`. `detailsUrl` is the console route for the
 * project's settings.
 *
 * The pane leads with a static card — the app's own Open Graph summary, the
 * same thing a chat client shows for a pasted link. It arrives in one cheap
 * request instead of a full page load in a frame, and it answers the question
 * most visits have ("is my app up, and is it mine?") without booting the app.
 * The live frame stays one click away.
 */
export function AppPreviewPane({ url, openUrl, detailsUrl, title, defaultOpen = false }: AppPreviewPaneProps) {
  const externalUrl = openUrl ?? url;
  const isGateUrl = openUrl !== undefined && openUrl !== url;
  const { t } = useT();
  const [open, setOpen] = useState(defaultOpen);
  const [card, setCard] = useState<SiteCard | null>(null);
  const [cardLoading, setCardLoading] = useState(true);
  const [viewport, setViewport] = useState<Viewport>("full");
  const [reloadKey, setReloadKey] = useState(0);
  const [checkedEmbeddable, setEmbeddable] = useState<boolean | null>(null);
  const [checkStatus, setCheckStatus] = useState<number | null>(null);
  const embeddable = isGateUrl ? true : checkedEmbeddable;
  const previewHost = hostOf(externalUrl);
  const checking = open && embeddable === null;
  const gatewayError = checkStatus !== null && GATEWAY_ERROR_STATUSES.has(checkStatus);

  function handleReload() {
    setEmbeddable(null);
    setCheckStatus(null);
    setReloadKey((k) => k + 1);
  }

  useEffect(() => {
    let cancelled = false;
    fetch(`/api/site-card?url=${encodeURIComponent(url)}`)
      .then((res) => res.json())
      .then((data: SiteCard) => {
        if (!cancelled) setCard(data);
      })
      .catch(() => {})
      .finally(() => {
        if (!cancelled) setCardLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [url, reloadKey]);

  useEffect(() => {
    if (!open || isGateUrl || checkedEmbeddable !== null) return;
    let cancelled = false;
    fetch(`/api/frame-check?url=${encodeURIComponent(url)}`)
      .then((res) => res.json())
      .then((data: { embeddable?: boolean; status?: number }) => {
        if (cancelled) return;
        setEmbeddable(data.embeddable ?? false);
        setCheckStatus(typeof data.status === "number" ? data.status : null);
      })
      .catch(() => {
        if (!cancelled) {
          setEmbeddable(false);
          setCheckStatus(null);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [open, url, checkedEmbeddable, isGateUrl]);

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

      <div className="border-t border-gray-100 dark:border-gray-800 p-4">
        <div className="flex flex-col gap-4 sm:flex-row">
          {card?.image ? (
            <img
              src={card.image}
              alt=""
              className="h-32 w-full shrink-0 rounded-lg border border-gray-200 dark:border-gray-800 object-cover sm:w-56"
            />
          ) : null}

          <div className="min-w-0 flex-1">
            {cardLoading && card === null ? (
              <div className="flex items-center gap-2 text-sm text-gray-400 dark:text-gray-500">
                <Spinner />
                {t("previewPane.card.loading")}
              </div>
            ) : (
              <>
                <p className="truncate text-sm font-semibold text-gray-900 dark:text-gray-100">
                  {card?.title ?? title ?? previewHost}
                </p>
                <p className="mt-1 line-clamp-2 text-xs text-gray-500 dark:text-gray-400">
                  {card?.description ??
                    (card && card.status !== 200
                      ? t("previewPane.card.down")
                      : t("previewPane.card.empty"))}
                </p>
                <p className="mt-2 truncate text-xs text-gray-400 dark:text-gray-500">{previewHost}</p>
              </>
            )}

            <div className="mt-3 flex flex-wrap items-center gap-2">
              <button
                type="button"
                onClick={() => setOpen((v) => !v)}
                className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 dark:border-gray-800 px-2.5 py-1.5 text-xs font-medium text-gray-600 dark:text-gray-300 hover:border-blue-300 hover:text-blue-600 transition-colors"
              >
                <Play className="h-3.5 w-3.5" />
                {open ? t("previewPane.card.hide") : t("previewPane.card.open")}
              </button>
              <Link
                href={detailsUrl}
                className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 dark:border-gray-800 px-2.5 py-1.5 text-xs font-medium text-gray-600 dark:text-gray-300 hover:border-blue-300 hover:text-blue-600 transition-colors"
              >
                {t("previewPane.projectDetails")}
              </Link>
              <a
                href={externalUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 dark:border-gray-800 px-2.5 py-1.5 text-xs font-medium text-gray-600 dark:text-gray-300 hover:border-blue-300 hover:text-blue-600 transition-colors"
              >
                <ExternalLink className="h-3.5 w-3.5" />
                {t("previewPane.openUi")}
              </a>
            </div>
          </div>
        </div>
      </div>

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
                href={externalUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 dark:border-gray-800 px-2.5 py-1.5 text-xs font-medium text-gray-600 dark:text-gray-300 hover:border-blue-300 hover:text-blue-600 transition-colors"
              >
                <ExternalLink className="h-3.5 w-3.5" />
                {t("previewPane.openUi")}
              </a>
            </div>
          </div>

          {checking ? (
            <div className="flex h-64 items-center justify-center rounded-lg border border-gray-100 dark:border-gray-800 bg-gray-50 dark:bg-gray-950">
              <Spinner />
              <span className="ml-2 text-sm text-gray-400 dark:text-gray-500">{t("previewPane.checking")}</span>
            </div>
          ) : gatewayError ? (
            <div className="flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-amber-300 dark:border-amber-800 bg-amber-50 dark:bg-amber-950/30 py-12 text-center">
              <AlertTriangle className="h-6 w-6 text-amber-500 dark:text-amber-400" />
              <p className="text-sm font-medium text-amber-800 dark:text-amber-200">{t("previewPane.gatewayError.title")}</p>
              <p className="max-w-sm text-xs text-amber-700 dark:text-amber-300">{t("previewPane.gatewayError.body")}</p>
              <a
                href={externalUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1.5 text-sm font-medium text-amber-700 dark:text-amber-300 hover:underline"
              >
                <ExternalLink className="h-4 w-4" />
                {t("previewPane.openUi")}
              </a>
            </div>
          ) : embeddable === false ? (
            <div className="flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-950 py-12 text-center">
              <p className="text-sm font-medium text-gray-700 dark:text-gray-200">{t("previewPane.blocked.title")}</p>
              <p className="max-w-sm text-xs text-gray-400 dark:text-gray-500">{t("previewPane.blocked.body")}</p>
              <a
                href={externalUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1.5 text-sm font-medium text-blue-600 dark:text-blue-400 hover:underline"
              >
                <ExternalLink className="h-4 w-4" />
                {t("previewPane.openUi")}
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
