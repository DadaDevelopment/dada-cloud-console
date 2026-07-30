"use client";
import { useRef, useState, DragEvent } from "react";
import { useRouter } from "next/navigation";
import { UploadCloud } from "lucide-react";
import { appsApi } from "@/lib/api";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";

const APP_NAME_RE = /^([a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])$/;
const ACCEPTED_EXT = [".zip", ".tar.gz", ".tgz"];

function hasAcceptedExtension(name: string): boolean {
  const lower = name.toLowerCase();
  return ACCEPTED_EXT.some((ext) => lower.endsWith(ext));
}

export interface UploadDeployCardProps {
  projectId: string;
  envId: string | null;
  compact?: boolean;
  hero?: boolean;
  className?: string;
}

/**
 * Second no-git onramp, next to the starter-template cards: drop an archive
 * exported from a vibe-coding tool (Lovable/Bolt/v0) and get a live URL without
 * ever touching GitHub. Flow: upload the archive, which detects framework/port,
 * stores it, and queues the first build; the app itself is materialized by that
 * build when it succeeds, with the detected port and worker flag. Redirects to
 * the build's log page on success.
 *
 * Do NOT reintroduce the old pre-create step (an app seeded with a pause
 * placeholder image so the upload endpoint would accept it): the pause container
 * is reported 1/1 Running within seconds, which showed the user a green Ready
 * badge and a surrogate domain over a build that was still running — or had
 * already failed.
 */
export function UploadDeployCard({ projectId, envId, compact, hero, className }: UploadDeployCardProps) {
  const { t } = useT();
  const router = useRouter();
  const inputRef = useRef<HTMLInputElement>(null);

  const [file, setFile] = useState<File | null>(null);
  const [appName, setAppName] = useState("");
  const [dragActive, setDragActive] = useState(false);
  const [progress, setProgress] = useState<number | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const trimmedName = appName.trim();
  const nameValid = trimmedName === "" || APP_NAME_RE.test(trimmedName);
  const canSubmit = !!file && trimmedName !== "" && nameValid && !submitting && !!envId;

  function pickFile(f: File | null) {
    if (!f) return;
    if (!hasAcceptedExtension(f.name)) {
      setError(t("apps.deploy.fromUpload.error.type"));
      return;
    }
    setError(null);
    setFile(f);
  }

  function onDrop(e: DragEvent<HTMLDivElement>) {
    e.preventDefault();
    setDragActive(false);
    pickFile(e.dataTransfer.files?.[0] ?? null);
  }

  async function handleSubmit() {
    if (!canSubmit || !file || !envId) return;
    setSubmitting(true);
    setError(null);
    setProgress(0);
    try {
      const result = await appsApi.uploadSourceArchive(projectId, envId, trimmedName, file, (pct) => setProgress(pct));
      router.push(`/projects/${projectId}/apps/${trimmedName}/builds/${result.build.id}?envId=${envId}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("apps.deploy.fromUpload.error.upload"));
      setSubmitting(false);
      setProgress(null);
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
            {t("apps.deploy.fromUpload.title")}
          </h2>
          <p
            className={
              hero
                ? "mt-2 text-sm text-gray-600 dark:text-gray-300 sm:text-base"
                : "mt-1 text-sm text-gray-500 dark:text-gray-400"
            }
          >
            {t("apps.deploy.fromUpload.desc")}
          </p>
        </div>
      )}

      <div className="mt-4 space-y-3">
        <div
          onDragOver={(e) => {
            e.preventDefault();
            setDragActive(true);
          }}
          onDragLeave={() => setDragActive(false)}
          onDrop={onDrop}
          onClick={() => inputRef.current?.click()}
          role="button"
          tabIndex={0}
          className={`flex cursor-pointer flex-col items-center justify-center gap-2 rounded-xl border-2 border-dashed px-4 py-8 text-center transition-colors ${
            dragActive
              ? "border-blue-500 bg-blue-50 dark:bg-blue-950/40"
              : "border-gray-300 dark:border-gray-700 hover:border-gray-400 dark:hover:border-gray-600"
          }`}
        >
          <UploadCloud className="h-6 w-6 text-gray-400 dark:text-gray-500" />
          <p className="text-sm font-medium text-gray-700 dark:text-gray-200">
            {file ? file.name : t("apps.deploy.fromUpload.dropzone.label")}
          </p>
          <p className="text-xs text-gray-400 dark:text-gray-500">{t("apps.deploy.fromUpload.dropzone.hint")}</p>
          <input
            ref={inputRef}
            type="file"
            accept=".zip,.tar.gz,.tgz"
            className="hidden"
            onChange={(e) => pickFile(e.target.files?.[0] ?? null)}
          />
        </div>

        <div>
          <input
            type="text"
            value={appName}
            onChange={(e) => setAppName(e.target.value)}
            placeholder={t("apps.deploy.fromUpload.name.placeholder")}
            aria-invalid={!nameValid || undefined}
            className={`block w-full rounded-lg border px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:outline-none focus:ring-1 ${
              !nameValid
                ? "border-red-400 dark:border-red-700 focus:border-red-500 focus:ring-red-500"
                : "border-gray-300 dark:border-gray-700 focus:border-blue-500 focus:ring-blue-500"
            }`}
          />
          {!nameValid && (
            <p role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">
              {t("apps.modal.create.name.invalid.format")}
            </p>
          )}
        </div>

        {progress !== null && (
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
            <div className="h-full rounded-full bg-blue-600 transition-all" style={{ width: `${progress}%` }} />
          </div>
        )}

        {error && (
          <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-3 py-2 text-sm text-red-700 dark:text-red-300">
            {error}
          </div>
        )}

        <button
          type="button"
          onClick={handleSubmit}
          disabled={!canSubmit}
          className="inline-flex w-full items-center justify-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-60"
        >
          {submitting && <Spinner size="sm" />}
          {submitting ? t("apps.deploy.fromUpload.submitting") : t("apps.deploy.fromUpload.submit")}
        </button>
      </div>
    </>
  );

  if (compact) {
    return <div className={className}>{body}</div>;
  }

  const containerClass = hero
    ? "rounded-2xl border-2 border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm sm:p-8"
    : "rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm";

  return <div className={`${containerClass} ${className ?? ""}`}>{body}</div>;
}
