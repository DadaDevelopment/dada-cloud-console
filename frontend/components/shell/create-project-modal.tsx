"use client";
import { FormEvent, useState } from "react";
import { projectsApi } from "@/lib/api";
import { useT } from "@/lib/i18n/console/context";

// SLUG_RE mirrors the backend projectSlugRe: a DNS-1123-label-safe slug, so the
// user gets instant feedback instead of a round-trip 400.
const SLUG_RE = /^[a-z][a-z0-9-]{1,38}[a-z0-9]$/;

const CYRILLIC_MAP: Record<string, string> = {
  а: "a", б: "b", в: "v", г: "g", д: "d", е: "e", ё: "e", ж: "zh", з: "z", и: "i", й: "y",
  к: "k", л: "l", м: "m", н: "n", о: "o", п: "p", р: "r", с: "s", т: "t", у: "u", ф: "f",
  х: "h", ц: "ts", ч: "ch", ш: "sh", щ: "sch", ъ: "", ы: "y", ь: "", э: "e", ю: "yu", я: "ya",
};

export function normalizeProjectSlug(value: string) {
  const transliterated = value
    .toLowerCase()
    .trim()
    .split("")
    .map((ch) => CYRILLIC_MAP[ch] ?? ch)
    .join("")
    .replace(/\s+/g, "-")
    .replace(/[^a-z0-9-]/g, "")
    .replace(/-+/g, "-")
    .replace(/^-+|-+$/g, "");

  if (SLUG_RE.test(transliterated)) return transliterated;

  const fallback = `project-${Math.abs(
    Array.from(value.trim()).reduce((hash, ch) => ((hash << 5) - hash + ch.codePointAt(0)!) | 0, 0)
  ).toString(36)}`;
  return fallback.slice(0, 40).replace(/-+$/g, "");
}

/**
 * Create-project dialog. Shared by the top-bar switcher and any other entry point
 * so the create flow lives in exactly one place.
 */
export function CreateProjectModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (projectId: string) => void;
}) {
  const { t } = useT();
  const [projectName, setProjectName] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const slug = normalizeProjectSlug(projectName);
  const canSubmit = projectName.trim().length > 0 && !submitting;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    try {
      const res = await projectsApi.create({
        slug,
        display_name: projectName.trim() || undefined,
      });
      onCreated(res.project_id);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("projects.error.create"));
      setSubmitting(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <div className="w-full max-w-md rounded-xl border border-gray-200 bg-white p-6 shadow-xl">
        <h2 className="text-lg font-semibold text-gray-900">{t("projects.modal.title")}</h2>
        <p className="mt-1 text-sm text-gray-600">{t("projects.modal.subtitle")}</p>

        <form onSubmit={submit} className="mt-5 space-y-4">
          <div>
            <label htmlFor="project_name" className="block text-sm font-medium text-gray-800">
              {t("projects.name.label")}
            </label>
            <input
              id="project_name"
              autoFocus
              value={projectName}
              onChange={(e) => setProjectName(e.target.value)}
              placeholder={t("projects.name.placeholder")}
              className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 placeholder-gray-400 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <p className="mt-1 text-xs text-gray-500">{t("projects.name.help")}</p>
          </div>

          {error && (
            <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
              {error}
            </div>
          )}

          <div className="flex items-center justify-end gap-2 pt-2">
            <button
              type="button"
              onClick={onClose}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100"
            >
              {t("common.cancel")}
            </button>
            <button
              type="submit"
              disabled={!canSubmit}
              className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {submitting ? t("common.creating") : t("projects.submit")}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
