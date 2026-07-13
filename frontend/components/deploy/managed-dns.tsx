"use client";
import { useCallback, useEffect, useRef, useState, FormEvent } from "react";
import { managedDnsApi, normalizeRecords } from "@/lib/api";
import type { ManagedZone, ManagedZoneRecord } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { StateChip } from "@/components/ui/state-chip";
import { useT } from "@/lib/i18n/console/context";

const RECORD_TYPES = ["A", "AAAA", "CNAME", "MX", "TXT", "SRV", "CAA"] as const;
const PROTECTED_TYPES = new Set(["NS", "SOA"]);
const POLL_INTERVAL_MS = 15_000;

function recordKey(r: { name: string; type: string }): string {
  return `${r.name}|${r.type}`;
}

function CopyRow({ value }: { value: string }) {
  const { t } = useT();
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex items-center gap-2">
      <code className="flex-1 break-all rounded-md border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900 px-3 py-2 text-xs text-gray-800 dark:text-gray-200">
        {value}
      </code>
      <button
        type="button"
        onClick={() => {
          navigator.clipboard.writeText(value).then(() => {
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          });
        }}
        className="rounded-md border border-gray-300 dark:border-gray-700 px-2 py-2 text-xs text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
      >
        {copied ? t("common.copied") : t("common.copy")}
      </button>
    </div>
  );
}

interface Props {
  projectId: string;
  authId: string;
  apex: string;
  canEdit: boolean;
}

interface RecordDraft {
  name: string;
  type: string;
  ttl: number;
  value: string;
}

const EMPTY_DRAFT: RecordDraft = { name: "", type: "A", ttl: 3600, value: "" };

/**
 * Managed-DNS panel: delegate a verified apex to our nameservers, watch the
 * delegation propagate, optionally import the domain's current records, and
 * edit the live zone through the powerdns-backed API.
 */
export function ManagedDnsPanel({ projectId, authId, apex, canEdit }: Props) {
  const { t } = useT();
  const [zone, setZone] = useState<ManagedZone | null>(null);
  const [loadingZone, setLoadingZone] = useState(true);
  const [zoneError, setZoneError] = useState<string | null>(null);
  const [delegating, setDelegating] = useState(false);

  const [records, setRecords] = useState<ManagedZoneRecord[]>([]);
  const [recordsError, setRecordsError] = useState<string | null>(null);

  const [importPreview, setImportPreview] = useState<ManagedZoneRecord[] | null>(null);
  const [importChecked, setImportChecked] = useState<Set<string>>(new Set());
  const [importing, setImporting] = useState(false);
  const [importResult, setImportResult] = useState<string | null>(null);
  const [importError, setImportError] = useState<string | null>(null);

  const [draft, setDraft] = useState<RecordDraft>(EMPTY_DRAFT);
  const [showForm, setShowForm] = useState(false);
  const [savingRecord, setSavingRecord] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [deletingKey, setDeletingKey] = useState<string | null>(null);

  const zoneRef = useRef<ManagedZone | null>(null);
  useEffect(() => {
    zoneRef.current = zone;
  }, [zone]);

  const loadRecords = useCallback(async () => {
    try {
      const data = await managedDnsApi.listRecords(projectId, authId);
      setRecords(normalizeRecords(data));
      setRecordsError(null);
    } catch (err) {
      setRecordsError(err instanceof Error ? err.message : t("domains.dns.recordsError"));
    }
  }, [projectId, authId, t]);

  const loadImportPreview = useCallback(async () => {
    try {
      const data = await managedDnsApi.importPreview(projectId, authId);
      const list = normalizeRecords(data);
      setImportPreview(list);
      setImportChecked(new Set(list.map(recordKey)));
    } catch {
      setImportPreview(null);
    }
  }, [projectId, authId]);

  useEffect(() => {
    let alive = true;
    const loadZone = async () => {
      setLoadingZone(true);
      setZoneError(null);
      setImportResult(null);
      void loadImportPreview();
      try {
        const z = await managedDnsApi.getZone(projectId, authId);
        if (!alive) return;
        setZone(z);
        void loadRecords();
      } catch (err) {
        if (!alive) return;
        const status = (err as { status?: number }).status;
        if (status === 404) {
          setZone(null);
        } else {
          setZoneError(err instanceof Error ? err.message : t("domains.dns.zoneError"));
        }
      } finally {
        if (alive) setLoadingZone(false);
      }
    };
    void loadZone();
    return () => {
      alive = false;
    };
  }, [projectId, authId, loadRecords, loadImportPreview, t]);

  useEffect(() => {
    if (!zone || zone.status === "active") return;
    const id = setInterval(() => {
      managedDnsApi
        .getZone(projectId, authId)
        .then((z) => {
          setZone(z);
          if (z.status === "active" && zoneRef.current?.status !== "active") void loadRecords();
        })
        .catch(() => undefined);
    }, POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, [projectId, authId, zone, loadRecords]);

  async function handleDelegate() {
    setDelegating(true);
    setZoneError(null);
    try {
      const res = await managedDnsApi.delegate(projectId, authId);
      setZone({ zone: res.zone, status: res.status, nameservers: res.nameservers });
      void loadRecords();
    } catch (err) {
      setZoneError(err instanceof Error ? err.message : t("domains.dns.delegateError"));
    } finally {
      setDelegating(false);
    }
  }

  function toggleImport(key: string) {
    setImportChecked((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  async function handleImport() {
    if (!importPreview) return;
    const selected = importPreview.filter((r) => importChecked.has(recordKey(r)));
    if (selected.length === 0) return;
    setImporting(true);
    setImportError(null);
    setImportResult(null);
    try {
      const res = await managedDnsApi.importRecords(projectId, authId, selected);
      setImportResult(t("domains.dns.importDone", { imported: String(res.imported), skipped: String(res.skipped) }));
      setImportPreview(null);
      void loadRecords();
    } catch (err) {
      setImportError(err instanceof Error ? err.message : t("domains.dns.importError"));
    } finally {
      setImporting(false);
    }
  }

  function startEdit(r: ManagedZoneRecord) {
    setDraft({ name: r.name, type: r.type, ttl: r.ttl, value: r.contents.join("\n") });
    setShowForm(true);
    setSaveError(null);
  }

  async function handleSaveRecord(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSavingRecord(true);
    setSaveError(null);
    const contents = draft.value
      .split("\n")
      .map((v) => v.trim())
      .filter((v) => v.length > 0);
    try {
      await managedDnsApi.upsertRecord(projectId, authId, {
        name: draft.name.trim(),
        type: draft.type,
        ttl: draft.ttl,
        contents,
      });
      setDraft(EMPTY_DRAFT);
      setShowForm(false);
      void loadRecords();
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : t("domains.dns.saveError"));
    } finally {
      setSavingRecord(false);
    }
  }

  async function handleDeleteRecord(r: ManagedZoneRecord) {
    if (!confirm(t("domains.dns.confirmDelete", { name: r.name, type: r.type }))) return;
    const key = recordKey(r);
    setDeletingKey(key);
    setRecordsError(null);
    try {
      await managedDnsApi.deleteRecord(projectId, authId, r.name, r.type);
      setRecords((prev) => prev.filter((x) => recordKey(x) !== key));
    } catch (err) {
      setRecordsError(err instanceof Error ? err.message : t("domains.dns.deleteError"));
    } finally {
      setDeletingKey(null);
    }
  }

  if (loadingZone) {
    return (
      <div className="flex h-24 items-center justify-center">
        <Spinner />
      </div>
    );
  }

  const inputClass =
    "w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500";

  return (
    <div className="space-y-6">
      {zoneError && (
        <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {zoneError}
        </div>
      )}

      {!zone ? (
        <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-6">
          <p className="text-sm text-gray-600 dark:text-gray-300">{t("domains.dns.intro")}</p>
          {importPreview && importPreview.length > 0 && (
            <ImportBlock
              records={importPreview}
              checked={importChecked}
              onToggle={toggleImport}
              onImport={handleImport}
              importing={importing}
              importError={importError}
              canEdit={canEdit}
            />
          )}
          {canEdit && (
            <button
              type="button"
              onClick={handleDelegate}
              disabled={delegating}
              className="mt-4 inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {delegating ? <Spinner size="sm" /> : null}
              {delegating ? t("domains.dns.delegating") : t("domains.dns.delegateBtn")}
            </button>
          )}
        </div>
      ) : (
        <>
          <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-5">
            <div className="flex items-center gap-3">
              <p className="font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{apex}</p>
              <StateChip tone={zone.status === "active" ? "ready" : "needs-action"} dot>
                {zone.status === "active" ? t("domains.dns.statusActive") : t("domains.dns.statusAwaiting")}
              </StateChip>
            </div>

            <p className="mt-4 text-sm font-medium text-gray-700 dark:text-gray-200">{t("domains.dns.nsTitle")}</p>
            <div className="mt-2 space-y-2">
              {zone.nameservers.map((ns) => (
                <CopyRow key={ns} value={ns} />
              ))}
            </div>
            <p className="mt-2 text-xs text-gray-500 dark:text-gray-400">{t("domains.dns.nsNote")}</p>

            {zone.status !== "active" && (
              <p className="mt-3 flex items-center gap-1.5 text-xs text-gray-400 dark:text-gray-500">
                <Spinner size="sm" /> {t("domains.dns.polling")}
              </p>
            )}
          </div>

          {importResult && (
            <div className="rounded-lg border border-green-200 dark:border-green-900 bg-green-50 dark:bg-green-950/40 px-4 py-3 text-sm text-green-700 dark:text-green-300">
              {importResult}
            </div>
          )}

          {importPreview && importPreview.length > 0 && (
            <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-5">
              <ImportBlock
                records={importPreview}
                checked={importChecked}
                onToggle={toggleImport}
                onImport={handleImport}
                importing={importing}
                importError={importError}
                canEdit={canEdit}
              />
            </div>
          )}

          <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900">
            <div className="flex items-center justify-between px-5 py-4">
              <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("domains.dns.recordsTitle")}</h3>
              {canEdit && (
                <button
                  type="button"
                  onClick={() => {
                    setDraft(EMPTY_DRAFT);
                    setSaveError(null);
                    setShowForm((v) => !v);
                  }}
                  className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-xs font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
                >
                  {t("domains.dns.addRecord")}
                </button>
              )}
            </div>

            {showForm && canEdit && (
              <form onSubmit={handleSaveRecord} className="border-t border-gray-100 dark:border-gray-800 px-5 py-4 space-y-3">
                <div className="grid gap-3 sm:grid-cols-4">
                  <select
                    value={draft.type}
                    onChange={(e) => setDraft((d) => ({ ...d, type: e.target.value }))}
                    className={inputClass}
                  >
                    {RECORD_TYPES.map((rt) => (
                      <option key={rt} value={rt}>
                        {rt}
                      </option>
                    ))}
                  </select>
                  <input
                    type="text"
                    required
                    value={draft.name}
                    onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
                    placeholder={t("domains.dns.namePlaceholder")}
                    className={inputClass}
                  />
                  <input
                    type="number"
                    min={60}
                    value={draft.ttl}
                    onChange={(e) => setDraft((d) => ({ ...d, ttl: Number(e.target.value) }))}
                    className={inputClass}
                  />
                  <button
                    type="submit"
                    disabled={savingRecord}
                    className="inline-flex items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors"
                  >
                    {savingRecord ? <Spinner size="sm" /> : null}
                    {savingRecord ? t("domains.dns.saving") : t("domains.dns.saveRecord")}
                  </button>
                </div>
                <textarea
                  required
                  rows={2}
                  value={draft.value}
                  onChange={(e) => setDraft((d) => ({ ...d, value: e.target.value }))}
                  placeholder={t("domains.dns.valuePlaceholder")}
                  className={inputClass}
                />
                {saveError && (
                  <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-2 text-sm text-red-700 dark:text-red-300">
                    {saveError}
                  </div>
                )}
              </form>
            )}

            {recordsError && (
              <div role="alert" className="mx-5 mb-4 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-2 text-sm text-red-700 dark:text-red-300">
                {recordsError}
              </div>
            )}

            {records.length === 0 ? (
              <p className="px-5 py-6 text-sm text-gray-500 dark:text-gray-400">{t("domains.dns.recordsEmpty")}</p>
            ) : (
              <div className="overflow-x-auto border-t border-gray-100 dark:border-gray-800">
                <table className="w-full text-sm">
                  <thead className="bg-gray-50 dark:bg-gray-900/60 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                    <tr>
                      <th className="px-5 py-3">{t("domains.dns.thType")}</th>
                      <th className="px-5 py-3">{t("domains.dns.thName")}</th>
                      <th className="px-5 py-3">{t("domains.dns.thTtl")}</th>
                      <th className="px-5 py-3">{t("domains.dns.thValue")}</th>
                      {canEdit && <th className="px-5 py-3" />}
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                    {records.map((r) => {
                      const key = recordKey(r);
                      const locked = PROTECTED_TYPES.has(r.type.toUpperCase());
                      return (
                        <tr key={key}>
                          <td className="px-5 py-3 font-mono text-gray-500 dark:text-gray-400">{r.type}</td>
                          <td className="px-5 py-3 font-mono text-gray-900 dark:text-gray-100">{r.name}</td>
                          <td className="px-5 py-3 text-gray-500 dark:text-gray-400">{r.ttl}</td>
                          <td className="px-5 py-3 font-mono text-xs text-gray-700 dark:text-gray-300">
                            {r.contents.join(", ")}
                          </td>
                          {canEdit && (
                            <td className="px-5 py-3 text-right">
                              {locked ? (
                                <span className="text-xs text-gray-400 dark:text-gray-600">{t("domains.dns.protectedNote")}</span>
                              ) : (
                                <div className="flex items-center justify-end gap-2">
                                  <button
                                    type="button"
                                    onClick={() => startEdit(r)}
                                    className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-xs font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
                                  >
                                    {t("domains.dns.edit")}
                                  </button>
                                  <button
                                    type="button"
                                    onClick={() => handleDeleteRecord(r)}
                                    disabled={deletingKey === key}
                                    className="rounded-lg border border-red-200 dark:border-red-900 px-3 py-1.5 text-xs font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/40 disabled:opacity-50 transition-colors"
                                  >
                                    {t("common.delete")}
                                  </button>
                                </div>
                              )}
                            </td>
                          )}
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}

function ImportBlock({
  records,
  checked,
  onToggle,
  onImport,
  importing,
  importError,
  canEdit,
}: {
  records: ManagedZoneRecord[];
  checked: Set<string>;
  onToggle: (key: string) => void;
  onImport: () => void;
  importing: boolean;
  importError: string | null;
  canEdit: boolean;
}) {
  const { t } = useT();
  return (
    <div className="mt-4">
      <p className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("domains.dns.importTitle")}</p>
      <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{t("domains.dns.importNote")}</p>
      <ul className="mt-3 space-y-1.5">
        {records.map((r) => {
          const key = `${r.name}|${r.type}`;
          return (
            <li key={key} className="flex items-center gap-3 rounded-lg border border-gray-200 dark:border-gray-800 px-3 py-2">
              <input
                type="checkbox"
                checked={checked.has(key)}
                onChange={() => onToggle(key)}
                disabled={!canEdit}
                className="h-4 w-4 rounded border-gray-300 dark:border-gray-700"
              />
              <span className="font-mono text-xs text-gray-500 dark:text-gray-400 w-14">{r.type}</span>
              <span className="font-mono text-xs text-gray-900 dark:text-gray-100 w-32 truncate">{r.name}</span>
              <span className="font-mono text-xs text-gray-600 dark:text-gray-300 flex-1 truncate">{r.contents.join(", ")}</span>
            </li>
          );
        })}
      </ul>
      {importError && (
        <div role="alert" className="mt-3 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-2 text-sm text-red-700 dark:text-red-300">
          {importError}
        </div>
      )}
      {canEdit && (
        <button
          type="button"
          onClick={onImport}
          disabled={importing || checked.size === 0}
          className="mt-3 inline-flex items-center gap-2 rounded-lg border border-gray-300 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-50 transition-colors"
        >
          {importing ? <Spinner size="sm" /> : null}
          {importing ? t("domains.dns.importing") : t("domains.dns.importBtn")}
        </button>
      )}
    </div>
  );
}
