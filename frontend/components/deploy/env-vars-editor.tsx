"use client";
import { useCallback, useEffect, useState, FormEvent } from "react";
import { envVarsApi } from "@/lib/api";
import type { EnvVar } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { CopyButton } from "@/components/ui/copy-button";
import { useT } from "@/lib/i18n/console/context";

type Scope = "build" | "runtime" | "both";

interface EditState {
  key: string;
  value: string;
  is_secret: boolean;
  scope: Scope;
  editingExisting: boolean;
}

const EMPTY: EditState = { key: "", value: "", is_secret: true, scope: "both", editingExisting: false };

export function EnvVarsEditor({
  projectId,
  envId,
  appName,
  canEdit,
}: {
  projectId: string;
  envId: string;
  appName: string;
  canEdit: boolean;
}) {
  const { t } = useT();
  const [vars, setVars] = useState<EnvVar[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [revealed, setRevealed] = useState<Record<string, string>>({});
  const [revealing, setRevealing] = useState<string | null>(null);

  const [modalOpen, setModalOpen] = useState(false);
  const [form, setForm] = useState<EditState>(EMPTY);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!envId) return;
    setLoading(true);
    try {
      const d = await envVarsApi.list(projectId, envId, appName);
      setVars(d.env_vars ?? []);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("apps.env.error.load"));
    } finally {
      setLoading(false);
    }
  }, [projectId, envId, appName, t]);

  useEffect(() => {
    void load(); // eslint-disable-line react-hooks/set-state-in-effect
  }, [load]);

  async function handleReveal(key: string) {
    setRevealing(key);
    try {
      const { value } = await envVarsApi.reveal(projectId, envId, appName, key);
      setRevealed((prev) => ({ ...prev, [key]: value }));
    } catch (err) {
      const msg = err instanceof Error ? err.message : t("apps.env.error.reveal");
      setError(/403|forbidden|permission/i.test(msg) ? t("apps.env.error.revealForbidden") : msg);
    } finally {
      setRevealing(null);
    }
  }

  function openAdd() {
    setForm(EMPTY);
    setSubmitError(null);
    setModalOpen(true);
  }

  function openEdit(v: EnvVar) {
    setForm({ key: v.key, value: "", is_secret: v.is_secret, scope: v.scope, editingExisting: true });
    setSubmitError(null);
    setModalOpen(true);
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setSubmitting(true);
    try {
      await envVarsApi.upsert(projectId, envId, appName, form.key, {
        value: form.value,
        is_secret: form.is_secret,
        scope: form.scope,
      });
      setModalOpen(false);
      setRevealed((prev) => {
        const next = { ...prev };
        delete next[form.key];
        return next;
      });
      await load();
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : t("apps.env.error.save"));
    } finally {
      setSubmitting(false);
    }
  }

  async function handleDelete(key: string) {
    if (!window.confirm(t("apps.env.confirmDelete", { key }))) return;
    setDeleting(key);
    try {
      await envVarsApi.remove(projectId, envId, appName, key);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("apps.env.error.delete"));
    } finally {
      setDeleting(null);
    }
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("apps.env.heading")}</h2>
          <p className="text-sm text-gray-600 dark:text-gray-400">{t("apps.env.subtitle")}</p>
        </div>
        {canEdit && (
          <button
            onClick={openAdd}
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 transition-colors"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            {t("apps.env.add")}
          </button>
        )}
      </div>

      {error && (
        <div className="mb-4 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">{error}</div>
      )}

      {loading ? (
        <div className="flex h-32 items-center justify-center">
          <Spinner />
        </div>
      ) : vars.length === 0 ? (
        <p className="rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/40 px-5 py-10 text-center text-sm text-gray-500 dark:text-gray-400">
          {t("apps.env.empty")}
        </p>
      ) : (
        <div className="overflow-x-auto rounded-xl border border-gray-200 dark:border-gray-800">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 dark:bg-gray-900/60 text-left text-xs font-semibold uppercase tracking-wide text-gray-600 dark:text-gray-400">
              <tr>
                <th className="px-4 py-2.5">{t("apps.env.col.key")}</th>
                <th className="px-4 py-2.5">{t("apps.env.col.value")}</th>
                <th className="px-4 py-2.5">{t("apps.env.col.scope")}</th>
                {canEdit && <th className="px-4 py-2.5 text-right">{t("apps.env.col.actions")}</th>}
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
              {vars.map((v) => (
                <tr key={v.id} className="bg-white dark:bg-gray-950">
                  <td className="px-4 py-3 font-mono font-medium text-gray-900 dark:text-gray-100">{v.key}</td>
                  <td className="px-4 py-3">
                    {v.is_secret ? (
                      revealed[v.key] !== undefined ? (
                        <span className="inline-flex items-center gap-2">
                          <code className="rounded bg-gray-100 px-2 py-0.5 font-mono text-xs text-gray-800">{revealed[v.key]}</code>
                          <CopyButton value={revealed[v.key]} />
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-2">
                          <span className="font-mono text-gray-400">••••••••</span>
                          {canEdit && (
                            <button
                              onClick={() => handleReveal(v.key)}
                              disabled={revealing === v.key}
                              className="text-xs text-blue-600 hover:text-blue-700 disabled:opacity-50"
                            >
                              {revealing === v.key ? t("apps.env.revealing") : t("apps.env.reveal")}
                            </button>
                          )}
                        </span>
                      )
                    ) : (
                      <span className="inline-flex items-center gap-2">
                        <code className="rounded bg-gray-100 px-2 py-0.5 font-mono text-xs text-gray-800">{v.value ?? ""}</code>
                        {v.value && <CopyButton value={v.value} />}
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-3">
                    <span className="inline-flex items-center rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-600">
                      {v.scope}
                    </span>
                    {v.is_secret && (
                      <span className="ml-1.5 inline-flex items-center rounded-full bg-amber-50 px-2 py-0.5 text-xs font-medium text-amber-700 ring-1 ring-amber-600/20">
                        {t("apps.env.secret")}
                      </span>
                    )}
                  </td>
                  {canEdit && (
                    <td className="px-4 py-3 text-right">
                      <div className="inline-flex gap-3">
                        <button onClick={() => openEdit(v)} className="text-xs text-blue-600 hover:text-blue-700">
                          {t("apps.env.edit")}
                        </button>
                        <button
                          onClick={() => handleDelete(v.key)}
                          disabled={deleting === v.key}
                          className="text-xs text-red-600 hover:text-red-700 disabled:opacity-50"
                        >
                          {deleting === v.key ? t("apps.env.deleting") : t("apps.env.delete")}
                        </button>
                      </div>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <Modal
        isOpen={modalOpen}
        onClose={() => {
          setModalOpen(false);
          setSubmitError(null);
        }}
        title={form.editingExisting ? t("apps.env.modal.editTitle", { key: form.key }) : t("apps.env.modal.addTitle")}
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.env.col.key")}</label>
            <input
              type="text"
              required
              value={form.key}
              disabled={form.editingExisting}
              onChange={(e) => setForm((f) => ({ ...f, key: e.target.value }))}
              placeholder="DATABASE_URL"
              pattern="[A-Za-z_][A-Za-z0-9_]*"
              title={t("apps.env.field.keyHint")}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 disabled:bg-gray-100 dark:disabled:bg-gray-800"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {t("apps.env.col.value")} {form.editingExisting && <span className="font-normal text-gray-500 dark:text-gray-400">{t("apps.env.field.valueReplaces")}</span>}
            </label>
            <input
              type="text"
              required
              value={form.value}
              onChange={(e) => setForm((f) => ({ ...f, value: e.target.value }))}
              placeholder="postgres://…"
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.env.col.scope")}</label>
            <select
              value={form.scope}
              onChange={(e) => setForm((f) => ({ ...f, scope: e.target.value as Scope }))}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="both">{t("apps.env.scope.both")}</option>
              <option value="build">{t("apps.env.scope.build")}</option>
              <option value="runtime">{t("apps.env.scope.runtime")}</option>
            </select>
          </div>

          <div className="flex items-center justify-between rounded-lg border border-gray-200 dark:border-gray-800 px-4 py-3">
            <div>
              <p className="text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.env.field.secret")}</p>
              <p className="text-xs text-gray-500 dark:text-gray-400">{t("apps.env.field.secretHint")}</p>
            </div>
            <button
              type="button"
              onClick={() => setForm((f) => ({ ...f, is_secret: !f.is_secret }))}
              className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 ${
                form.is_secret ? "bg-blue-600" : "bg-gray-200"
              }`}
              role="switch"
              aria-checked={form.is_secret}
            >
              <span className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${form.is_secret ? "translate-x-6" : "translate-x-1"}`} />
            </button>
          </div>

          {submitError && (
            <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
              {submitError}
            </div>
          )}

          <div className="flex justify-end gap-3 pt-1">
            <button
              type="button"
              onClick={() => {
                setModalOpen(false);
                setSubmitError(null);
              }}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
            >
              {t("common.cancel")}
            </button>
            <button
              type="submit"
              disabled={submitting}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors"
            >
              {submitting ? <><Spinner size="sm" /> {t("apps.env.saving")}</> : t("apps.env.save")}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
