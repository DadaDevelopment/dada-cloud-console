"use client";
import { useRef, useState, DragEvent, ChangeEvent } from "react";
import { useRouter } from "next/navigation";
import { UploadCloud } from "lucide-react";
import { appsApi } from "@/lib/api";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";
import { buildZip, isExcludedPath, type ZipInputEntry } from "@/lib/zip";

const APP_NAME_RE = /^([a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])$/;
const ACCEPTED_EXT = [".zip", ".tar.gz", ".tgz"];
const MAX_UPLOAD_BYTES = 100 * 1024 * 1024;

function hasAcceptedExtension(name: string): boolean {
  const lower = name.toLowerCase();
  return ACCEPTED_EXT.some((ext) => lower.endsWith(ext));
}

function slugifyAppName(raw: string): string | null {
  const slug = raw
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 63);
  return slug && APP_NAME_RE.test(slug) ? slug : null;
}

/** A source is either an archive sent as-is, or a set of raw files packed client-side into a zip. */
type PickedSource =
  | { kind: "archive"; file: File }
  | { kind: "tree"; files: { path: string; file: File }[]; excludedCount: number; rootName: string | null };

interface TreeFile {
  path: string;
  file: File;
}

/**
 * FileSystemEntry / FileSystemDirectoryEntry are DOM types not in the
 * default TS lib; the drag-and-drop directory API is typed loosely here and
 * guarded with a runtime capability check (webkitGetAsEntry) before use.
 */
interface FsEntryLike {
  isFile?: boolean;
  isDirectory?: boolean;
  name: string;
  file?(cb: (f: File) => void, errCb: (e: unknown) => void): void;
  createReader?(): { readEntries(cb: (entries: FsEntryLike[]) => void, errCb: (e: unknown) => void): void };
}

async function readEntryRecursive(entry: FsEntryLike, prefix: string, out: TreeFile[]): Promise<void> {
  if (entry.isFile && entry.file) {
    const file = await new Promise<File>((resolve, reject) => entry.file!(resolve, reject));
    out.push({ path: prefix + entry.name, file });
    return;
  }
  if (entry.isDirectory && entry.createReader) {
    const reader = entry.createReader();
    let batch: FsEntryLike[];
    do {
      batch = await new Promise<FsEntryLike[]>((resolve, reject) => reader.readEntries(resolve, reject));
      for (const child of batch) {
        await readEntryRecursive(child, `${prefix}${entry.name}/`, out);
      }
    } while (batch.length > 0);
  }
}

async function readDroppedTree(items: DataTransferItemList): Promise<TreeFile[] | null> {
  const entries: FsEntryLike[] = [];
  for (let i = 0; i < items.length; i++) {
    const item = items[i];
    if (item.kind !== "file") continue;
    const getEntry = (item as unknown as { webkitGetAsEntry?: () => FsEntryLike | null }).webkitGetAsEntry;
    if (typeof getEntry !== "function") return null;
    const entry = getEntry.call(item);
    if (entry) entries.push(entry);
  }
  if (entries.length === 0) return null;
  const out: TreeFile[] = [];
  for (const entry of entries) {
    await readEntryRecursive(entry, "", out);
  }
  return out;
}

function commonRootName(paths: string[]): string | null {
  let root: string | null = null;
  for (const p of paths) {
    const idx = p.indexOf("/");
    if (idx < 0) return null;
    const top = p.slice(0, idx);
    if (root === null) root = top;
    else if (root !== top) return null;
  }
  return root;
}

function filterExcluded(files: TreeFile[]): { kept: TreeFile[]; excludedCount: number } {
  const kept = files.filter((f) => !isExcludedPath(f.path));
  return { kept, excludedCount: files.length - kept.length };
}

/** Non-standard attributes that enable directory picking on a hidden file input; not in React's DOM types. */
const DIRECTORY_INPUT_PROPS: Record<string, string> = { webkitdirectory: "", directory: "" };

export interface UploadDeployCardProps {
  projectId: string;
  envId: string | null;
  compact?: boolean;
  hero?: boolean;
  className?: string;
}

/**
 * Second no-git onramp, next to the starter-template cards: drop an archive,
 * a folder, or a single file exported from a vibe-coding tool (Lovable/Bolt/
 * v0) and get a live URL without ever touching GitHub or a terminal zip
 * command. A folder or a lone file is packed into a zip client-side
 * (frontend/lib/zip.ts) and sent through the same endpoint an archive drop
 * already used; the backend detects format purely from magic bytes
 * (backend/internal/api/uploadsource.go), so a client-built zip needs no
 * server change. Flow: upload, which detects framework/port, stores it, and
 * queues the first build; the app itself is materialized by that build when
 * it succeeds, with the detected port and worker flag. Redirects to the
 * build's log page on success.
 *
 * Do NOT reintroduce the old pre-create step (an app seeded with a pause
 * placeholder image so the upload endpoint would accept it): the pause
 * container is reported 1/1 Running within seconds, which showed the user a
 * green Ready badge and a surrogate domain over a build that was still
 * running — or had already failed.
 */
export function UploadDeployCard({ projectId, envId, compact, hero, className }: UploadDeployCardProps) {
  const { t } = useT();
  const router = useRouter();
  const inputRef = useRef<HTMLInputElement>(null);
  const folderInputRef = useRef<HTMLInputElement>(null);

  const [source, setSource] = useState<PickedSource | null>(null);
  const [appName, setAppName] = useState("");
  const [dragActive, setDragActive] = useState(false);
  const [packingCount, setPackingCount] = useState<number | null>(null);
  const [progress, setProgress] = useState<number | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const trimmedName = appName.trim();
  const nameValid = trimmedName === "" || APP_NAME_RE.test(trimmedName);
  const canSubmit = !!source && trimmedName !== "" && nameValid && !submitting && packingCount === null && !!envId;

  function maybePrefillName(rootName: string | null) {
    if (appName.trim() !== "" || !rootName) return;
    const slug = slugifyAppName(rootName);
    if (slug) setAppName(slug);
  }

  function pickArchive(f: File) {
    setError(null);
    setSource({ kind: "archive", file: f });
  }

  function pickTree(raw: TreeFile[]) {
    const { kept, excludedCount } = filterExcluded(raw);
    if (kept.length === 0) {
      setError(t("apps.deploy.fromUpload.error.empty"));
      return;
    }
    const totalBytes = kept.reduce((sum, f) => sum + f.file.size, 0);
    if (totalBytes > MAX_UPLOAD_BYTES) {
      setError(t("apps.deploy.fromUpload.error.tooLarge", { size: (totalBytes / (1024 * 1024)).toFixed(1) }));
      return;
    }
    const rootName = commonRootName(kept.map((f) => f.path));
    setError(null);
    setSource({ kind: "tree", files: kept, excludedCount, rootName });
    maybePrefillName(rootName);
  }

  function pickSingleFile(f: File) {
    if (hasAcceptedExtension(f.name)) {
      pickArchive(f);
      return;
    }
    pickTree([{ path: f.name, file: f }]);
  }

  function onInputFileChange(e: ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0] ?? null;
    if (!f) return;
    pickSingleFile(f);
    e.target.value = "";
  }

  function onFolderInputChange(e: ChangeEvent<HTMLInputElement>) {
    const list = e.target.files;
    if (!list || list.length === 0) return;
    const files: TreeFile[] = Array.from(list).map((f) => ({
      path: (f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name,
      file: f,
    }));
    pickTree(files);
    e.target.value = "";
  }

  async function onDrop(e: DragEvent<HTMLDivElement>) {
    e.preventDefault();
    setDragActive(false);

    const items = e.dataTransfer.items;
    if (items && items.length > 0) {
      const tree = await readDroppedTree(items);
      if (tree && tree.length > 0) {
        if (tree.length === 1 && !tree[0].path.includes("/") && hasAcceptedExtension(tree[0].path)) {
          pickArchive(tree[0].file);
        } else {
          pickTree(tree);
        }
        return;
      }
      if (tree && tree.length === 0) {
        setError(t("apps.deploy.fromUpload.error.empty"));
        return;
      }
    }

    const files = e.dataTransfer.files;
    if (!files || files.length === 0) return;
    if (files.length === 1) {
      pickSingleFile(files[0]);
      return;
    }
    pickTree(Array.from(files).map((f) => ({ path: f.name, file: f })));
  }

  /** Resolves the archive File to upload: as-is for a dropped archive, or freshly zipped for a folder/file tree. */
  async function resolveUploadFile(): Promise<File> {
    if (!source) throw new Error("no source selected");
    if (source.kind === "archive") return source.file;

    setPackingCount(source.files.length);
    try {
      const entries: ZipInputEntry[] = [];
      for (const { path, file } of source.files) {
        const buf = await file.arrayBuffer();
        entries.push({ path, data: new Uint8Array(buf) });
      }
      const zipBytes = buildZip(entries);
      if (zipBytes.length > MAX_UPLOAD_BYTES) {
        throw new Error(t("apps.deploy.fromUpload.error.tooLarge", { size: (zipBytes.length / (1024 * 1024)).toFixed(1) }));
      }
      const zipBuffer = zipBytes.buffer.slice(zipBytes.byteOffset, zipBytes.byteOffset + zipBytes.byteLength) as ArrayBuffer;
      const name = `${trimmedName || "upload"}.zip`;
      return new File([zipBuffer], name, { type: "application/zip" });
    } finally {
      setPackingCount(null);
    }
  }

  async function handleSubmit() {
    if (!canSubmit || !source || !envId) return;
    setSubmitting(true);
    setError(null);
    setProgress(0);
    try {
      const uploadFile = await resolveUploadFile();
      const result = await appsApi.uploadSourceArchive(projectId, envId, trimmedName, uploadFile, (pct) => setProgress(pct));
      router.push(`/projects/${projectId}/apps/${trimmedName}/builds/${result.build.id}?envId=${envId}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("apps.deploy.fromUpload.error.upload"));
      setSubmitting(false);
      setProgress(null);
    }
  }

  function sourceLabel(): string {
    if (!source) return t("apps.deploy.fromUpload.dropzone.label");
    if (source.kind === "archive") return source.file.name;
    if (source.rootName) {
      return t("apps.deploy.fromUpload.folderPicked", {
        count: source.files.length,
        name: source.rootName,
        excluded: source.excludedCount,
      });
    }
    return t("apps.deploy.fromUpload.filesPicked", { count: source.files.length });
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
          <p className="text-sm font-medium text-gray-700 dark:text-gray-200">{sourceLabel()}</p>
          <p className="text-xs text-gray-400 dark:text-gray-500">{t("apps.deploy.fromUpload.dropzone.hint")}</p>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              folderInputRef.current?.click();
            }}
            className="text-xs font-medium text-blue-600 hover:underline dark:text-blue-400"
          >
            {t("apps.deploy.fromUpload.pickFolder")}
          </button>
          <input
            ref={inputRef}
            type="file"
            accept=".zip,.tar.gz,.tgz"
            className="hidden"
            onChange={onInputFileChange}
          />
          <input
            ref={folderInputRef}
            type="file"
            className="hidden"
            {...DIRECTORY_INPUT_PROPS}
            onClick={(e) => e.stopPropagation()}
            onChange={onFolderInputChange}
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

        {packingCount !== null && (
          <p className="flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400">
            <Spinner size="sm" />
            {t("apps.deploy.fromUpload.packing", { count: packingCount })}
          </p>
        )}

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
          data-ux="apps_deploy_upload:submit"
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
