"use client";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { filesApi } from "@/lib/api";
import type { AppFileEntry, AppFileListResponse } from "@/lib/types";
import { formatBytes } from "@/components/charts/format";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";
import { FileEditor } from "./file-editor";

interface FileBrowserProps {
  projectId: string;
  envId: string;
  appName: string;
  canWrite: boolean;
  initialPath?: string;
}

interface Toast {
  id: number;
  kind: "success" | "error";
  text: string;
}

interface OpenFile {
  path: string;
  name: string;
  content: string;
  modified: number;
  dirty: boolean;
}

type PreviewKind = "editor" | "image" | "blocked";

const IMAGE_EXTENSIONS = new Set(["png", "jpg", "jpeg", "gif", "webp", "svg", "avif", "bmp", "ico"]);
const TRUNCATION_LIMIT = 2000;

function extensionOf(name: string): string {
  const dot = name.lastIndexOf(".");
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : "";
}

function isImage(name: string): boolean {
  return IMAGE_EXTENSIONS.has(extensionOf(name));
}

function joinPath(dir: string, name: string): string {
  return dir === "/" ? `/${name}` : `${dir}/${name}`;
}

function parentPath(dir: string): string {
  if (dir === "/") return "/";
  const cut = dir.lastIndexOf("/");
  return cut <= 0 ? "/" : dir.slice(0, cut);
}

function formatTimestamp(unixSeconds: number, locale: string): string {
  if (!unixSeconds) return "—";
  return new Date(unixSeconds * 1000).toLocaleString(locale === "ru" ? "ru-RU" : "en-GB", {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/**
 * Full-height file manager for one app's persistent volume: a directory list on
 * the left, an editor or preview on the right. Every path it sends is relative
 * to the volume root, which is what the API returns as well.
 */
export function FileBrowser({ projectId, envId, appName, canWrite, initialPath = "/" }: FileBrowserProps) {
  const { t, locale } = useT();

  const [cwd, setCwd] = useState(initialPath);
  const [listing, setListing] = useState<AppFileListResponse | null>(null);
  const [listLoading, setListLoading] = useState(true);
  const [listError, setListError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");

  const [open, setOpen] = useState<OpenFile | null>(null);
  const [previewKind, setPreviewKind] = useState<PreviewKind>("editor");
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);
  const [blockedReason, setBlockedReason] = useState<string | null>(null);
  const [fileLoading, setFileLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [conflict, setConflict] = useState(false);

  const [newFolderOpen, setNewFolderOpen] = useState(false);
  const [newFolderName, setNewFolderName] = useState("");
  const [renameTarget, setRenameTarget] = useState<AppFileEntry | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<AppFileEntry | null>(null);
  const [modalBusy, setModalBusy] = useState(false);
  const [modalError, setModalError] = useState<string | null>(null);

  const [uploads, setUploads] = useState<Record<string, number>>({});
  const [dragging, setDragging] = useState(false);
  const dragDepth = useRef(0);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const [toasts, setToasts] = useState<Toast[]>([]);
  const toastCounter = useRef(0);

  const pushToast = useCallback((kind: Toast["kind"], text: string) => {
    const id = ++toastCounter.current;
    setToasts((prev) => [...prev, { id, kind, text }]);
    setTimeout(() => setToasts((prev) => prev.filter((x) => x.id !== id)), 4000);
  }, []);

  const describeError = useCallback(
    (e: unknown, fallbackKey = "apps.files.error.generic"): string => {
      const err = e as (Error & { status?: number }) | undefined;
      const message = err?.message ?? "";
      if (message.includes("has no shell")) return t("apps.files.error.noShell");
      if (message.includes("no running pod")) return t("apps.files.error.noPod");
      if (message.includes("app has no volume")) return t("apps.files.error.noVolume");
      if (err?.status === 403) return t("apps.files.error.noPermission");
      return message || t(fallbackKey);
    },
    [t]
  );

  const loadDir = useCallback(
    async (dir: string) => {
      setListLoading(true);
      setListError(null);
      try {
        const res = await filesApi.list(projectId, envId, appName, dir);
        setListing(res);
        setCwd(res.path);
      } catch (e) {
        setListing(null);
        setListError(describeError(e));
      } finally {
        setListLoading(false);
      }
    },
    [projectId, envId, appName, describeError]
  );

  useEffect(() => {
    if (!envId) return;
    let cancelled = false;
    void (async () => {
      await Promise.resolve();
      if (!cancelled) await loadDir(initialPath);
    })();
    return () => {
      cancelled = true;
    };
  }, [envId, initialPath, loadDir]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const url = new URL(window.location.href);
    url.searchParams.set("path", cwd);
    window.history.replaceState(null, "", url.toString());
  }, [cwd]);

  useEffect(() => {
    return () => {
      if (previewUrl) URL.revokeObjectURL(previewUrl);
    };
  }, [previewUrl]);

  const clearPreview = useCallback(() => {
    setPreviewUrl((prev) => {
      if (prev) URL.revokeObjectURL(prev);
      return null;
    });
  }, []);

  const openEntry = useCallback(
    async (entry: AppFileEntry) => {
      const target = joinPath(cwd, entry.name);
      if (entry.kind === "dir") {
        setFilter("");
        void loadDir(target);
        return;
      }

      setFileLoading(true);
      setConflict(false);
      setBlockedReason(null);
      clearPreview();

      if (isImage(entry.name)) {
        setPreviewKind("image");
        setOpen({ path: target, name: entry.name, content: "", modified: entry.modified, dirty: false });
        try {
          setPreviewUrl(await filesApi.objectUrl(projectId, envId, appName, target));
        } catch (e) {
          setPreviewKind("blocked");
          setBlockedReason(describeError(e));
        } finally {
          setFileLoading(false);
        }
        return;
      }

      try {
        const res = await filesApi.read(projectId, envId, appName, target);
        setPreviewKind("editor");
        setOpen({
          path: res.path,
          name: entry.name,
          content: res.content,
          modified: res.modified,
          dirty: false,
        });
      } catch (e) {
        const err = e as Error & { status?: number };
        setPreviewKind("blocked");
        setOpen({ path: target, name: entry.name, content: "", modified: entry.modified, dirty: false });
        if (err.status === 415) setBlockedReason(t("apps.files.editor.binary"));
        else if (err.status === 413) setBlockedReason(t("apps.files.editor.tooLarge"));
        else setBlockedReason(describeError(e));
      } finally {
        setFileLoading(false);
      }
    },
    [appName, clearPreview, cwd, describeError, envId, loadDir, projectId, t]
  );

  const closeFile = useCallback(() => {
    clearPreview();
    setOpen(null);
    setBlockedReason(null);
    setConflict(false);
  }, [clearPreview]);

  const save = useCallback(
    async (force: boolean) => {
      if (!open || !canWrite || previewKind !== "editor") return;
      setSaving(true);
      try {
        const res = await filesApi.write(projectId, envId, appName, {
          path: open.path,
          content: open.content,
          modified: force ? 0 : open.modified,
        });
        setOpen((prev) => (prev ? { ...prev, modified: res.modified, dirty: false } : prev));
        setConflict(false);
        pushToast("success", t("apps.files.saved"));
        void loadDir(cwd);
      } catch (e) {
        const err = e as Error & { status?: number };
        if (err.status === 409 && !force) {
          setConflict(true);
        } else {
          pushToast("error", describeError(e));
        }
      } finally {
        setSaving(false);
      }
    },
    [appName, canWrite, cwd, describeError, envId, loadDir, open, previewKind, projectId, pushToast, t]
  );

  const openRef = useRef(open);
  const saveRef = useRef(save);
  useEffect(() => {
    openRef.current = open;
    saveRef.current = save;
  });

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
        if (!openRef.current) return;
        e.preventDefault();
        void saveRef.current(false);
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);

  const uploadFiles = useCallback(
    async (fileList: FileList | File[]) => {
      const items = Array.from(fileList);
      for (const file of items) {
        setUploads((prev) => ({ ...prev, [file.name]: 0 }));
        try {
          await filesApi.upload(projectId, envId, appName, cwd, file, (percent) =>
            setUploads((prev) => ({ ...prev, [file.name]: percent }))
          );
          pushToast("success", t("apps.files.uploaded", { name: file.name }));
        } catch (e) {
          const err = e as Error & { status?: number };
          pushToast(
            "error",
            err.status === 413 ? t("apps.files.error.uploadTooLarge") : describeError(e)
          );
        } finally {
          setUploads((prev) => {
            const next = { ...prev };
            delete next[file.name];
            return next;
          });
        }
      }
      void loadDir(cwd);
    },
    [appName, cwd, describeError, envId, loadDir, projectId, pushToast, t]
  );

  const createFolder = useCallback(async () => {
    const name = newFolderName.trim();
    if (!name) return;
    setModalBusy(true);
    setModalError(null);
    try {
      await filesApi.mkdir(projectId, envId, appName, joinPath(cwd, name));
      setNewFolderOpen(false);
      setNewFolderName("");
      pushToast("success", t("apps.files.created"));
      void loadDir(cwd);
    } catch (e) {
      setModalError(describeError(e));
    } finally {
      setModalBusy(false);
    }
  }, [appName, cwd, describeError, envId, loadDir, newFolderName, projectId, pushToast, t]);

  const renameEntry = useCallback(async () => {
    if (!renameTarget) return;
    const name = renameValue.trim();
    if (!name || name === renameTarget.name) {
      setRenameTarget(null);
      return;
    }
    setModalBusy(true);
    setModalError(null);
    try {
      const from = joinPath(cwd, renameTarget.name);
      await filesApi.move(projectId, envId, appName, from, joinPath(cwd, name));
      if (open?.path === from) closeFile();
      setRenameTarget(null);
      pushToast("success", t("apps.files.renamed"));
      void loadDir(cwd);
    } catch (e) {
      setModalError(describeError(e));
    } finally {
      setModalBusy(false);
    }
  }, [appName, closeFile, cwd, describeError, envId, loadDir, open, projectId, pushToast, renameTarget, renameValue, t]);

  const removeEntry = useCallback(async () => {
    if (!deleteTarget) return;
    setModalBusy(true);
    setModalError(null);
    try {
      const target = joinPath(cwd, deleteTarget.name);
      await filesApi.remove(projectId, envId, appName, target, deleteTarget.kind === "dir");
      if (open?.path === target) closeFile();
      setDeleteTarget(null);
      pushToast("success", t("apps.files.deleted", { name: deleteTarget.name }));
      void loadDir(cwd);
    } catch (e) {
      setModalError(describeError(e));
    } finally {
      setModalBusy(false);
    }
  }, [appName, closeFile, cwd, deleteTarget, describeError, envId, loadDir, open, projectId, pushToast, t]);

  const download = useCallback(
    async (entry: AppFileEntry) => {
      const target = joinPath(cwd, entry.name);
      try {
        if (entry.kind === "dir") await filesApi.downloadDirectory(projectId, envId, appName, target);
        else await filesApi.downloadFile(projectId, envId, appName, target);
      } catch (e) {
        pushToast("error", describeError(e));
      }
    },
    [appName, cwd, describeError, envId, projectId, pushToast]
  );

  const downloadCurrentDir = useCallback(async () => {
    try {
      await filesApi.downloadDirectory(projectId, envId, appName, cwd);
    } catch (e) {
      pushToast("error", describeError(e));
    }
  }, [appName, cwd, describeError, envId, projectId, pushToast]);

  const entries = useMemo(() => {
    const all = listing?.entries ?? [];
    const needle = filter.trim().toLowerCase();
    const matched = needle ? all.filter((e) => e.name.toLowerCase().includes(needle)) : all;
    return [...matched].sort((a, b) => {
      const aDir = a.kind === "dir" ? 0 : 1;
      const bDir = b.kind === "dir" ? 0 : 1;
      if (aDir !== bDir) return aDir - bDir;
      return a.name.localeCompare(b.name, locale === "ru" ? "ru" : "en");
    });
  }, [filter, listing, locale]);

  const crumbs = useMemo(() => {
    const parts = cwd.split("/").filter(Boolean);
    return parts.map((part, i) => ({ name: part, path: `/${parts.slice(0, i + 1).join("/")}` }));
  }, [cwd]);

  const uploadEntries = Object.entries(uploads);

  function onDrop(e: React.DragEvent) {
    e.preventDefault();
    dragDepth.current = 0;
    setDragging(false);
    if (!canWrite) return;
    if (e.dataTransfer.files.length > 0) void uploadFiles(e.dataTransfer.files);
  }

  return (
    <div
      className="flex min-h-[70vh] flex-col"
      onDragEnter={(e) => {
        e.preventDefault();
        dragDepth.current += 1;
        if (canWrite) setDragging(true);
      }}
      onDragOver={(e) => e.preventDefault()}
      onDragLeave={(e) => {
        e.preventDefault();
        dragDepth.current -= 1;
        if (dragDepth.current <= 0) setDragging(false);
      }}
      onDrop={onDrop}
    >
      <div className="flex flex-wrap items-center gap-2 border-b border-gray-200 dark:border-gray-800 pb-3">
        <nav className="flex min-w-0 flex-1 flex-wrap items-center gap-1 text-sm">
          <button
            onClick={() => loadDir("/")}
            className="rounded-md px-2 py-1 font-medium text-gray-600 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-800"
          >
            {appName}
          </button>
          {crumbs.map((crumb, i) => (
            <span key={crumb.path} className="flex items-center gap-1">
              <span className="text-gray-300 dark:text-gray-600">/</span>
              <button
                onClick={() => loadDir(crumb.path)}
                className={`rounded-md px-2 py-1 hover:bg-gray-100 dark:hover:bg-gray-800 ${
                  i === crumbs.length - 1
                    ? "font-medium text-gray-900 dark:text-gray-100"
                    : "text-gray-500 dark:text-gray-400"
                }`}
              >
                {crumb.name}
              </button>
            </span>
          ))}
        </nav>

        <div className="flex items-center gap-2">
          <input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={t("apps.files.searchPlaceholder")}
            className="w-40 rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-1.5 text-sm text-gray-900 dark:text-gray-100 placeholder:text-gray-400 focus:border-blue-500 focus:outline-none"
          />
          <button
            onClick={() => loadDir(cwd)}
            title={t("apps.files.refresh")}
            className="rounded-lg border border-gray-300 dark:border-gray-700 px-2.5 py-1.5 text-sm text-gray-600 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
              />
            </svg>
          </button>
          <button
            onClick={downloadCurrentDir}
            className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800"
          >
            {t("apps.files.downloadDir")}
          </button>
          {canWrite && (
            <>
              <button
                onClick={() => {
                  setModalError(null);
                  setNewFolderName("");
                  setNewFolderOpen(true);
                }}
                className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800"
              >
                {t("apps.files.newFolder")}
              </button>
              <button
                onClick={() => fileInputRef.current?.click()}
                className="rounded-lg bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700"
              >
                {t("apps.files.upload")}
              </button>
              <input
                ref={fileInputRef}
                type="file"
                multiple
                className="hidden"
                onChange={(e) => {
                  if (e.target.files?.length) void uploadFiles(e.target.files);
                  e.target.value = "";
                }}
              />
            </>
          )}
        </div>
      </div>

      {uploadEntries.length > 0 && (
        <div className="mt-3 space-y-2">
          {uploadEntries.map(([name, percent]) => (
            <div key={name} className="rounded-lg bg-gray-50 dark:bg-gray-950 px-3 py-2">
              <div className="flex items-center justify-between text-xs text-gray-600 dark:text-gray-300">
                <span className="truncate font-mono">{name}</span>
                <span>{percent}%</span>
              </div>
              <div className="mt-1.5 h-1 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-gray-800">
                <div className="h-full rounded-full bg-blue-600 transition-all" style={{ width: `${percent}%` }} />
              </div>
            </div>
          ))}
        </div>
      )}

      <div className="relative mt-4 grid flex-1 gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
        <div className="min-w-0 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900">
          {listLoading ? (
            <div className="flex h-64 items-center justify-center">
              <Spinner size="lg" />
            </div>
          ) : listError ? (
            <div className="p-6">
              <p className="text-sm text-amber-700 dark:text-amber-400">{listError}</p>
            </div>
          ) : (
            <>
              <div className="grid grid-cols-[minmax(0,1fr)_5rem_9rem_2.5rem] items-center gap-2 border-b border-gray-100 dark:border-gray-800 px-4 py-2 text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">
                <span>{t("apps.files.column.name")}</span>
                <span className="text-right">{t("apps.files.column.size")}</span>
                <span className="text-right">{t("apps.files.column.modified")}</span>
                <span />
              </div>

              {cwd !== "/" && (
                <button
                  onClick={() => loadDir(parentPath(cwd))}
                  className="flex w-full items-center gap-2 px-4 py-2 text-left text-sm text-gray-500 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-800/50"
                >
                  <span className="font-mono">..</span>
                </button>
              )}

              {entries.length === 0 ? (
                <div className="flex h-56 flex-col items-center justify-center gap-1 text-center">
                  <p className="text-sm font-medium text-gray-600 dark:text-gray-300">
                    {filter ? t("apps.files.noMatches") : t("apps.files.empty")}
                  </p>
                  {!filter && canWrite && (
                    <p className="text-xs text-gray-400 dark:text-gray-500">{t("apps.files.emptyHint")}</p>
                  )}
                </div>
              ) : (
                <ul className="divide-y divide-gray-50 dark:divide-gray-800/60">
                  {entries.map((entry) => {
                    const active = open?.path === joinPath(cwd, entry.name);
                    return (
                      <li
                        key={entry.name}
                        className={`group grid grid-cols-[minmax(0,1fr)_5rem_9rem_2.5rem] items-center gap-2 px-4 py-2 text-sm ${
                          active ? "bg-blue-50 dark:bg-blue-950/30" : "hover:bg-gray-50 dark:hover:bg-gray-800/50"
                        }`}
                      >
                        <button
                          onClick={() => void openEntry(entry)}
                          className="flex min-w-0 items-center gap-2 text-left"
                          title={entry.mode}
                        >
                          <EntryIcon kind={entry.kind} name={entry.name} />
                          <span
                            className={`truncate ${
                              entry.kind === "dir"
                                ? "font-medium text-gray-900 dark:text-gray-100"
                                : "text-gray-700 dark:text-gray-300"
                            }`}
                          >
                            {entry.name}
                          </span>
                        </button>
                        <span className="text-right text-xs tabular-nums text-gray-400 dark:text-gray-500">
                          {entry.kind === "dir" ? "—" : formatBytes(entry.size)}
                        </span>
                        <span className="text-right text-xs tabular-nums text-gray-400 dark:text-gray-500">
                          {formatTimestamp(entry.modified, locale)}
                        </span>
                        <EntryMenu
                          canWrite={canWrite}
                          onDownload={() => void download(entry)}
                          onRename={() => {
                            setModalError(null);
                            setRenameValue(entry.name);
                            setRenameTarget(entry);
                          }}
                          onDelete={() => {
                            setModalError(null);
                            setDeleteTarget(entry);
                          }}
                        />
                      </li>
                    );
                  })}
                </ul>
              )}

              {listing?.truncated && (
                <p className="border-t border-gray-100 dark:border-gray-800 px-4 py-2 text-xs text-amber-600 dark:text-amber-500">
                  {t("apps.files.truncated", { count: TRUNCATION_LIMIT })}
                </p>
              )}
            </>
          )}
        </div>

        <div className="min-w-0 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900">
          {!open ? (
            <div className="flex h-full min-h-64 flex-col items-center justify-center gap-2 px-6 text-center">
              <p className="text-sm text-gray-500 dark:text-gray-400">{t("apps.files.subtitle")}</p>
              {!canWrite && (
                <p className="text-xs text-gray-400 dark:text-gray-500">{t("apps.files.editor.readonly")}</p>
              )}
            </div>
          ) : (
            <div className="flex h-full flex-col">
              <div className="flex flex-wrap items-center justify-between gap-2 border-b border-gray-100 dark:border-gray-800 px-4 py-2.5">
                <div className="min-w-0">
                  <p className="truncate font-mono text-sm text-gray-900 dark:text-gray-100">{open.name}</p>
                  <p className="truncate text-xs text-gray-400 dark:text-gray-500">{open.path}</p>
                </div>
                <div className="flex items-center gap-2">
                  {open.dirty && (
                    <span className="text-xs text-amber-600 dark:text-amber-500">{t("apps.files.editor.unsaved")}</span>
                  )}
                  {previewKind === "editor" && canWrite && (
                    <button
                      onClick={() => void save(false)}
                      disabled={!open.dirty || saving}
                      className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40"
                    >
                      {saving ? <Spinner size="sm" /> : null}
                      {saving ? t("apps.files.saving") : t("apps.files.save")}
                    </button>
                  )}
                  <button
                    onClick={closeFile}
                    className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm text-gray-600 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800"
                  >
                    {t("apps.files.close")}
                  </button>
                </div>
              </div>

              {conflict && (
                <div className="flex flex-wrap items-center gap-3 border-b border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-2.5 text-xs text-amber-800 dark:text-amber-300">
                  <span className="flex-1">{t("apps.files.editor.conflict")}</span>
                  <button
                    onClick={() => void save(true)}
                    className="rounded-md border border-amber-300 dark:border-amber-800 px-2 py-1 font-medium hover:bg-amber-100 dark:hover:bg-amber-900/40"
                  >
                    {t("apps.files.editor.overwrite")}
                  </button>
                </div>
              )}

              <div className="min-h-0 flex-1">
                {fileLoading ? (
                  <div className="flex h-64 items-center justify-center">
                    <Spinner size="lg" />
                  </div>
                ) : previewKind === "image" ? (
                  <div className="flex h-full items-center justify-center bg-[repeating-conic-gradient(#f3f4f6_0_25%,transparent_0_50%)] bg-[length:16px_16px] p-6 dark:bg-[repeating-conic-gradient(#1f2937_0_25%,transparent_0_50%)]">
                    {previewUrl && (
                      <img
                        src={previewUrl}
                        alt={open.name}
                        className="max-h-[60vh] max-w-full rounded-lg object-contain shadow"
                      />
                    )}
                  </div>
                ) : previewKind === "blocked" ? (
                  <div className="flex h-64 flex-col items-center justify-center gap-3 px-6 text-center">
                    <p className="text-sm text-gray-600 dark:text-gray-300">{blockedReason}</p>
                    <button
                      onClick={() =>
                        void download({ name: open.name, kind: "file", size: 0, modified: open.modified, mode: "" })
                      }
                      className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800"
                    >
                      {t("apps.files.download")}
                    </button>
                  </div>
                ) : (
                  <>
                    <FileEditor
                      key={open.path}
                      filename={open.name}
                      value={open.content}
                      readOnly={!canWrite}
                      height="min(60vh, 640px)"
                      onChange={(content) =>
                        setOpen((prev) => (prev ? { ...prev, content, dirty: true } : prev))
                      }
                    />
                    {canWrite && (
                      <p className="border-t border-gray-100 dark:border-gray-800 px-4 py-2 text-xs text-gray-400 dark:text-gray-500">
                        {t("apps.files.editor.hint")}
                      </p>
                    )}
                  </>
                )}
              </div>
            </div>
          )}
        </div>

        {dragging && (
          <div className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center rounded-xl border-2 border-dashed border-blue-500 bg-blue-50/80 dark:bg-blue-950/60">
            <p className="text-sm font-medium text-blue-700 dark:text-blue-300">{t("apps.files.dropHere")}</p>
          </div>
        )}
      </div>

      <Modal isOpen={newFolderOpen} onClose={() => setNewFolderOpen(false)} title={t("apps.files.newFolder.title")}>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
          {t("apps.files.newFolder.label")}
        </label>
        <input
          autoFocus
          value={newFolderName}
          onChange={(e) => setNewFolderName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") void createFolder();
          }}
          className="mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 font-mono text-sm text-gray-900 dark:text-gray-100"
        />
        {modalError && <p className="mt-2 text-sm text-red-600 dark:text-red-400">{modalError}</p>}
        <div className="mt-5 flex justify-end gap-2">
          <button
            onClick={() => setNewFolderOpen(false)}
            className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm text-gray-600 dark:text-gray-300"
          >
            {t("apps.files.cancel")}
          </button>
          <button
            onClick={() => void createFolder()}
            disabled={modalBusy || !newFolderName.trim()}
            className="rounded-lg bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40"
          >
            {t("apps.files.create")}
          </button>
        </div>
      </Modal>

      <Modal isOpen={!!renameTarget} onClose={() => setRenameTarget(null)} title={t("apps.files.rename.title")}>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
          {t("apps.files.rename.label")}
        </label>
        <input
          autoFocus
          value={renameValue}
          onChange={(e) => setRenameValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") void renameEntry();
          }}
          className="mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 font-mono text-sm text-gray-900 dark:text-gray-100"
        />
        {modalError && <p className="mt-2 text-sm text-red-600 dark:text-red-400">{modalError}</p>}
        <div className="mt-5 flex justify-end gap-2">
          <button
            onClick={() => setRenameTarget(null)}
            className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm text-gray-600 dark:text-gray-300"
          >
            {t("apps.files.cancel")}
          </button>
          <button
            onClick={() => void renameEntry()}
            disabled={modalBusy || !renameValue.trim()}
            className="rounded-lg bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40"
          >
            {t("apps.files.rename")}
          </button>
        </div>
      </Modal>

      <Modal isOpen={!!deleteTarget} onClose={() => setDeleteTarget(null)} title={t("apps.files.delete.title")}>
        <p className="text-sm text-gray-600 dark:text-gray-300">
          {deleteTarget?.kind === "dir"
            ? t("apps.files.delete.dirBody", { name: deleteTarget?.name ?? "" })
            : t("apps.files.delete.body", { name: deleteTarget?.name ?? "" })}
        </p>
        {modalError && <p className="mt-2 text-sm text-red-600 dark:text-red-400">{modalError}</p>}
        <div className="mt-5 flex justify-end gap-2">
          <button
            onClick={() => setDeleteTarget(null)}
            className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm text-gray-600 dark:text-gray-300"
          >
            {t("apps.files.cancel")}
          </button>
          <button
            onClick={() => void removeEntry()}
            disabled={modalBusy}
            className="rounded-lg bg-red-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-40"
          >
            {t("apps.files.delete.confirm")}
          </button>
        </div>
      </Modal>

      <div role="status" aria-live="polite" className="fixed bottom-6 right-6 z-50 flex flex-col gap-2">
        {toasts.map((toast) => (
          <div
            key={toast.id}
            className={`rounded-lg px-4 py-3 text-sm font-medium shadow-lg ${
              toast.kind === "success" ? "bg-green-600 text-white" : "bg-red-600 text-white"
            }`}
          >
            {toast.text}
          </div>
        ))}
      </div>
    </div>
  );
}

function EntryIcon({ kind, name }: { kind: AppFileEntry["kind"]; name: string }) {
  if (kind === "dir") {
    return (
      <svg className="h-4 w-4 shrink-0 text-blue-500" fill="currentColor" viewBox="0 0 20 20">
        <path d="M2 5a2 2 0 012-2h4l2 2h6a2 2 0 012 2v6a2 2 0 01-2 2H4a2 2 0 01-2-2V5z" />
      </svg>
    );
  }
  if (kind === "symlink") {
    return (
      <svg className="h-4 w-4 shrink-0 text-purple-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth={2}
          d="M13.828 10.172a4 4 0 010 5.656l-3 3a4 4 0 11-5.656-5.656l1.5-1.5m5.656-5.656l1.5-1.5a4 4 0 115.656 5.656l-3 3a4 4 0 01-5.656 0"
        />
      </svg>
    );
  }
  const color = isImage(name) ? "text-emerald-500" : "text-gray-400 dark:text-gray-500";
  return (
    <svg className={`h-4 w-4 shrink-0 ${color}`} fill="none" viewBox="0 0 24 24" stroke="currentColor">
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth={2}
        d="M7 21h10a2 2 0 002-2V9.414a1 1 0 00-.293-.707l-5.414-5.414A1 1 0 0012.586 3H7a2 2 0 00-2 2v14a2 2 0 002 2z"
      />
    </svg>
  );
}

function EntryMenu({
  canWrite,
  onDownload,
  onRename,
  onDelete,
}: {
  canWrite: boolean;
  onDownload: () => void;
  onRename: () => void;
  onDelete: () => void;
}) {
  const { t } = useT();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, [open]);

  return (
    <div ref={ref} className="relative flex justify-end">
      <button
        onClick={() => setOpen((v) => !v)}
        aria-label={t("apps.files.column.name")}
        className="rounded-md p-1 text-gray-400 opacity-0 transition-opacity hover:bg-gray-100 hover:text-gray-600 group-hover:opacity-100 dark:hover:bg-gray-800 dark:hover:text-gray-300"
      >
        <svg className="h-4 w-4" fill="currentColor" viewBox="0 0 20 20">
          <path d="M10 6a2 2 0 110-4 2 2 0 010 4zM10 12a2 2 0 110-4 2 2 0 010 4zM10 18a2 2 0 110-4 2 2 0 010 4z" />
        </svg>
      </button>
      {open && (
        <div className="absolute right-0 top-7 z-30 w-40 overflow-hidden rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-900 py-1 shadow-lg">
          <MenuItem
            label={t("apps.files.download")}
            onClick={() => {
              setOpen(false);
              onDownload();
            }}
          />
          {canWrite && (
            <>
              <MenuItem
                label={t("apps.files.rename")}
                onClick={() => {
                  setOpen(false);
                  onRename();
                }}
              />
              <MenuItem
                danger
                label={t("apps.files.delete")}
                onClick={() => {
                  setOpen(false);
                  onDelete();
                }}
              />
            </>
          )}
        </div>
      )}
    </div>
  );
}

function MenuItem({ label, onClick, danger }: { label: string; onClick: () => void; danger?: boolean }) {
  return (
    <button
      onClick={onClick}
      className={`block w-full px-3 py-1.5 text-left text-sm hover:bg-gray-50 dark:hover:bg-gray-800 ${
        danger ? "text-red-600 dark:text-red-400" : "text-gray-700 dark:text-gray-300"
      }`}
    >
      {label}
    </button>
  );
}
