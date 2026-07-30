"use client";
import { useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { appsApi } from "@/lib/api";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";

const ACCEPTED_EXT = [".zip", ".tar.gz", ".tgz"];

function hasAcceptedExtension(name: string): boolean {
  const lower = name.toLowerCase();
  return ACCEPTED_EXT.some((ext) => lower.endsWith(ext));
}

export interface ArchiveReuploadControlProps {
  projectId: string;
  envId: string;
  appName: string;
  className?: string;
}

/**
 * Re-upload control for an app whose source is an uploaded archive
 * (git_repos.provider = 'archive'). Used both in Settings -> Git (the
 * canonical home for source-management) and inline in Settings -> Config,
 * since users who deployed by archive land on the image-tag form looking
 * for a way to ship new source and find only a tag field there. One shared
 * component so both spots stay in sync instead of drifting into two copies.
 *
 * On success it hands the user straight to the new build's log page, the
 * same handoff UploadDeployCard uses for the initial upload.
 */
export function ArchiveReuploadControl({ projectId, envId, appName, className }: ArchiveReuploadControlProps) {
  const { t } = useT();
  const router = useRouter();
  const inputRef = useRef<HTMLInputElement>(null);

  const [progress, setProgress] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleFile(file: File | null) {
    if (!file || !envId) return;
    if (!hasAcceptedExtension(file.name)) {
      setError(t("apps.settings.source.reupload.error.type"));
      return;
    }
    setBusy(true);
    setError(null);
    setProgress(0);
    try {
      const result = await appsApi.uploadSourceArchive(projectId, envId, appName, file, (pct) => setProgress(pct));
      router.push(`/projects/${projectId}/apps/${appName}/builds/${result.build.id}?envId=${envId}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : t("apps.settings.source.reupload.error.upload"));
      setBusy(false);
      setProgress(null);
    }
  }

  return (
    <div className={className}>
      <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">
        {t("apps.settings.source.reupload.title")}
      </h3>
      <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("apps.settings.source.reupload.subtitle")}</p>
      <input
        ref={inputRef}
        type="file"
        accept=".zip,.tar.gz,.tgz"
        className="hidden"
        onChange={(e) => handleFile(e.target.files?.[0] ?? null)}
      />
      <button
        onClick={() => inputRef.current?.click()}
        disabled={busy}
        className="mt-3 rounded-lg border border-gray-300 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50 disabled:opacity-50"
      >
        {busy && <Spinner size="sm" />}
        {busy ? t("apps.settings.source.reupload.uploading") : t("apps.settings.source.reupload.button")}
      </button>
      {progress !== null && (
        <div className="mt-3 h-1.5 w-full max-w-xs overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
          <div className="h-full rounded-full bg-blue-600 transition-all" style={{ width: `${progress}%` }} />
        </div>
      )}
      {error && (
        <p role="alert" className="mt-2 text-sm text-red-600 dark:text-red-400">
          {error}
        </p>
      )}
    </div>
  );
}
