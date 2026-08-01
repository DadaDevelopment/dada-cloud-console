"use client";
import { Fingerprint } from "lucide-react";
import { useT } from "@/lib/i18n/console/context";

/**
 * Presentational half of the passkey offer: the modal itself, with no
 * knowledge of when it should appear or what enrollment does. Split out from
 * {@link ../passkey/passkey-prompt.PasskeyPrompt} so the visual can be
 * rendered on its own.
 */
export function PasskeyCard({
  busy,
  onLater,
  onCreate,
}: {
  busy: boolean;
  onLater: () => void;
  onCreate: () => void;
}) {
  const { t } = useT();

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="passkey-prompt-title"
      className="fixed inset-0 z-[70] flex items-center justify-center bg-black/60 p-4"
    >
      <div className="w-full max-w-lg rounded-2xl border border-gray-200 bg-white p-8 text-center shadow-2xl dark:border-gray-800 dark:bg-gray-900">
        <h2
          id="passkey-prompt-title"
          className="text-2xl font-bold text-gray-900 dark:text-gray-100"
        >
          {t("passkey.title")}
        </h2>
        <p className="mt-4 text-sm text-gray-600 dark:text-gray-300">{t("passkey.subtitle")}</p>

        <div className="my-8 flex justify-center">
          <div className="rounded-xl border-2 border-lime-500 p-3">
            <Fingerprint className="h-14 w-14 text-lime-500" strokeWidth={1.25} />
          </div>
        </div>

        <p className="text-sm text-gray-700 dark:text-gray-200">{t("passkey.body")}</p>
        <p className="mt-4 text-xs text-gray-500 dark:text-gray-400">{t("passkey.note")}</p>

        <div className="mt-8 flex justify-end gap-3">
          <button
            type="button"
            onClick={onLater}
            disabled={busy}
            className="rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-200 dark:hover:bg-gray-800"
          >
            {t("passkey.later")}
          </button>
          <button
            type="button"
            onClick={onCreate}
            disabled={busy}
            className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-500 disabled:opacity-50"
          >
            {t("passkey.create")}
          </button>
        </div>
      </div>
    </div>
  );
}
