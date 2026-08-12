"use client";

import { useEffect, useState } from "react";
import { usePathname } from "next/navigation";
import { Modal } from "@/components/ui/modal";
import { feedbackApi } from "@/lib/api";
import { useAuth } from "@/lib/auth-provider";
import { useT } from "@/lib/i18n/console/context";
import type { MyFeedbackItem } from "@/lib/types";
import { feedbackAgeParts, feedbackFirstLine, feedbackStatusBadgeClass, feedbackStatusLabelKey } from "@/lib/feedback-status";

const SUPPORT_EMAIL = "development@dada-tuda.ru";

function SupportIcon() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} className="h-5 w-5" aria-hidden="true">
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        d="M8.625 12a.375.375 0 11-.75 0 .375.375 0 01.75 0zm3.75 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm3.75 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zM21 12c0 4.556-4.03 8.25-9 8.25a9.764 9.764 0 01-2.555-.337A5.972 5.972 0 015.41 20.97a5.969 5.969 0 01-.474-.065 4.48 4.48 0 00.978-2.025c.09-.457-.133-.901-.467-1.226C3.93 16.178 3 14.189 3 12c0-4.556 4.03-8.25 9-8.25s9 3.694 9 8.25z"
      />
    </svg>
  );
}

type Status = "idle" | "sending" | "success" | "error";

/**
 * Compact "my tickets" list rendered under the feedback form. Refetched after
 * a successful submit (via `refreshKey`) so the user sees their own new ticket
 * without leaving the modal. Failures here stay silent -- the submit form must
 * keep working even if this list can't load.
 */
function MyTickets({ refreshKey }: { refreshKey: number }) {
  const { t } = useT();
  const [items, setItems] = useState<MyFeedbackItem[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    feedbackApi
      .mine()
      .then((data) => {
        if (!cancelled) setItems(data.items ?? []);
      })
      .catch(() => {
        if (!cancelled) setItems(null);
      });
    return () => {
      cancelled = true;
    };
  }, [refreshKey]);

  if (items === null) return null;

  return (
    <div className="mt-4 border-t border-gray-200 pt-3 dark:border-gray-800">
      <h3 className="mb-2 text-xs font-medium text-gray-500 dark:text-gray-400">{t("feedback.mine.heading")}</h3>
      {items.length === 0 ? (
        <p className="text-xs text-gray-400 dark:text-gray-500">{t("feedback.mine.empty")}</p>
      ) : (
        <ul className="space-y-2">
          {items.map((item) => {
            const { unit, count } = feedbackAgeParts(item.created_at);
            return (
              <li key={item.id} className="rounded-lg border border-gray-200 px-3 py-2 text-xs dark:border-gray-800">
                <div className="mb-1 flex flex-wrap items-center gap-2">
                  <span className={`rounded px-1.5 py-0.5 font-medium ${feedbackStatusBadgeClass(item.status)}`}>
                    {t(feedbackStatusLabelKey(item.status))}
                  </span>
                  <span className="text-gray-400 dark:text-gray-500">{t(`feedback.mine.age.${unit}`, { count: String(count) })}</span>
                  {item.app_name ? <span className="font-mono text-gray-400 dark:text-gray-500">{item.app_name}</span> : null}
                </div>
                <p className="text-gray-700 dark:text-gray-300">{feedbackFirstLine(item.message)}</p>
                {item.resolution ? (
                  <p className="mt-1 text-gray-500 dark:text-gray-400">
                    <span className="font-medium">{t("feedback.mine.resolutionLabel")}</span> {item.resolution}
                  </p>
                ) : null}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

/**
 * Unobtrusive floating support button in the authed console shell. Opens a
 * small modal that posts feedback to /api/v1/feedback (readable by the
 * support routine via psql). A mailto fallback stays available inside the
 * modal for anyone who prefers email.
 */
export function SupportButton() {
  const { t } = useT();
  const { user } = useAuth();
  const pathname = usePathname();
  const [open, setOpen] = useState(false);
  const [message, setMessage] = useState("");
  const [status, setStatus] = useState<Status>("idle");
  const [ticketsRefreshKey, setTicketsRefreshKey] = useState(0);

  function close() {
    setOpen(false);
    setMessage("");
    setStatus("idle");
  }

  async function submit() {
    const trimmed = message.trim();
    if (!trimmed || status === "sending") return;
    setStatus("sending");
    try {
      await feedbackApi.submit(trimmed, pathname ?? "");
      setStatus("success");
      setTicketsRefreshKey((k) => k + 1);
      setTimeout(close, 1500);
    } catch {
      setStatus("error");
    }
  }

  const mailBody = `Route: ${pathname ?? "unknown"}`;
  const mailto = `mailto:${SUPPORT_EMAIL}?subject=${encodeURIComponent(
    t("feedback.support.mailSubject"),
  )}&body=${encodeURIComponent(mailBody)}`;

  return (
    <>
      <button
        onClick={() => setOpen(true)}
        title={t("feedback.support.label")}
        aria-label={t("feedback.support.label")}
        className="fixed bottom-4 left-4 z-40 flex h-11 w-11 items-center justify-center rounded-full border border-gray-200 bg-white text-gray-600 shadow-lg transition-colors hover:bg-gray-50 hover:text-gray-900 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-300 dark:hover:bg-gray-800 dark:hover:text-white"
      >
        <SupportIcon />
      </button>

      <Modal isOpen={open} onClose={close} title={t("feedback.widget.title")}>
        {status === "success" ? (
          <div className="flex items-center gap-2 py-4 text-sm text-green-700 dark:text-green-400">
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4.5 12.75l6 6 9-13.5" />
            </svg>
            {t("feedback.widget.success")}
          </div>
        ) : (
          <div className="space-y-3">
            <textarea
              autoFocus
              value={message}
              onChange={(e) => setMessage(e.target.value)}
              placeholder={t("feedback.widget.placeholder")}
              rows={4}
              maxLength={4000}
              disabled={status === "sending"}
              className="w-full resize-none rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 placeholder-gray-400 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-gray-500"
            />

            {status === "error" && (
              <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-300">
                {t("feedback.widget.error")}
              </div>
            )}

            <div className="flex items-center justify-between gap-2">
              <a href={mailto} className="text-xs text-gray-500 underline hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200">
                {t("feedback.support.label")}: {SUPPORT_EMAIL}
              </a>
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  onClick={close}
                  className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-800"
                >
                  {t("common.cancel")}
                </button>
                <button
                  type="button"
                  onClick={submit}
                  disabled={!message.trim() || status === "sending"}
                  className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {status === "sending"
                    ? t("feedback.widget.sending")
                    : status === "error"
                      ? t("feedback.widget.retry")
                      : t("feedback.widget.submit")}
                </button>
              </div>
            </div>
          </div>
        )}

        {user ? <MyTickets refreshKey={ticketsRefreshKey} /> : null}
      </Modal>
    </>
  );
}
