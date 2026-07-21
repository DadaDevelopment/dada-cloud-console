"use client";

import { useState } from "react";
import { usePathname } from "next/navigation";
import { Modal } from "@/components/ui/modal";
import { feedbackApi } from "@/lib/api";
import { useT } from "@/lib/i18n/console/context";

function FeedbackIcon() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} className="h-5 w-5" aria-hidden="true">
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        d="M7.5 8.25h9m-9 3.75h6m-9 8.25l-.259-1.55A5.98 5.98 0 016.75 15v-3.75a5.25 5.25 0 015.25-5.25h4.5a5.25 5.25 0 015.25 5.25v3.75a5.25 5.25 0 01-5.25 5.25H8.25l-2.25 2.25z"
      />
    </svg>
  );
}

type Status = "idle" | "sending" | "success" | "error";

export function FeedbackWidget() {
  const { t } = useT();
  const pathname = usePathname();
  const [open, setOpen] = useState(false);
  const [message, setMessage] = useState("");
  const [status, setStatus] = useState<Status>("idle");

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
      setTimeout(close, 1500);
    } catch {
      setStatus("error");
    }
  }

  return (
    <>
      <button
        onClick={() => setOpen(true)}
        title={t("feedback.widget.button")}
        aria-label={t("feedback.widget.button")}
        className="fixed bottom-4 right-4 z-40 flex h-11 w-11 items-center justify-center rounded-full border border-gray-200 bg-white text-gray-600 shadow-lg transition-colors hover:bg-gray-50 hover:text-gray-900 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-300 dark:hover:bg-gray-800 dark:hover:text-white"
      >
        <FeedbackIcon />
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

            <div className="flex items-center justify-end gap-2">
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
        )}
      </Modal>
    </>
  );
}
