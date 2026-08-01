"use client";
import { useCallback, useEffect, useState, FormEvent } from "react";
import { useParams } from "next/navigation";
import { boxesApi } from "@/lib/api";
import type { Box, BoxConnect, BoxSession } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { CopyButton } from "@/components/ui/copy-button";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { ResourceZeroState } from "@/components/ui/resource-zero-state";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeUntil } from "@/lib/format";
import { Boxes as BoxesIcon } from "lucide-react";
import { useT } from "@/lib/i18n/console/context";

const TTL_CHOICES = [3600, 14400, 43200];

/**
 * Boxes: the console's own door to `box up`.
 *
 * Until this page existed the only way to get a box was the REST API or an MCP
 * client, which meant the product could be demonstrated but not USED by anyone
 * who arrived through the landing page. The gate on every Box go-to-market item
 * is "an outsider can create a box in production", and a person who has to write
 * a curl call with a bearer token is not that person.
 *
 * The create call is synchronous and can take two minutes on a cold pool, so the
 * button stays in its pending state for the whole wait rather than optimistically
 * closing: a box that is not yet ready has no coordinates to show, and showing a
 * row that says "Booting" would hide the one-time token behind a refresh the
 * customer cannot know to make.
 */
export default function BoxesPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const { t } = useT();
  const { project, role } = useProjectContext();

  const [boxes, setBoxes] = useState<Box[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [refreshTick, setRefreshTick] = useState(0);

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [name, setName] = useState("");
  const [ttl, setTtl] = useState(TTL_CHOICES[0]);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const [connectBox, setConnectBox] = useState<string | null>(null);
  const [connect, setConnect] = useState<BoxConnect | null>(null);
  const [session, setSession] = useState<BoxSession | null>(null);
  const [readyMs, setReadyMs] = useState<{ ms: number; pool: string } | null>(null);
  const [busyBox, setBusyBox] = useState<string | null>(null);

  const load = useCallback(() => {
    boxesApi
      .list(projectId)
      .then((data) => setBoxes(data.boxes ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : t("boxes.error.load")))
      .finally(() => setIsLoading(false));
  }, [projectId, t]);

  useEffect(() => {
    load();
  }, [load, refreshTick]);

  useEffect(() => {
    const settling = boxes.some((b) => b.status === "Booting" || b.status === "Deleting" || b.status === "Waking");
    if (!settling) return;
    const id = setTimeout(() => setRefreshTick((v) => v + 1), 4000);
    return () => clearTimeout(id);
  }, [boxes]);

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setIsSubmitting(true);
    try {
      const res = await boxesApi.up(projectId, {
        name: name.trim() || undefined,
        ttl_seconds: ttl,
        wait_seconds: 120,
      });
      setIsModalOpen(false);
      setName("");
      setConnectBox(res.box.name);
      setConnect(res.connect);
      setSession(res.session);
      setReadyMs({ ms: res.ready.time_to_ready_ms, pool: res.ready.pool });
      setRefreshTick((v) => v + 1);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : "failed");
    } finally {
      setIsSubmitting(false);
    }
  }

  async function openConnect(boxName: string, newSession: boolean) {
    setBusyBox(boxName);
    try {
      const res = await boxesApi.connection(projectId, boxName, newSession);
      setConnectBox(boxName);
      setConnect(res.connect);
      setSession(res.session ?? null);
      setReadyMs(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "failed");
    } finally {
      setBusyBox(null);
    }
  }

  async function act(boxName: string, action: "suspend" | "resume" | "remove") {
    if (action === "remove" && !window.confirm(t("boxes.action.deleteConfirm", { name: boxName }))) return;
    setBusyBox(boxName);
    try {
      await boxesApi[action](projectId, boxName);
      setRefreshTick((v) => v + 1);
    } catch (err) {
      setError(err instanceof Error ? err.message : "failed");
    } finally {
      setBusyBox(null);
    }
  }

  const canCreate = canMutate(role);

  return (
    <div>
      <div className="mb-8 flex flex-wrap items-start justify-between gap-3">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.projects"), href: "/projects" },
              { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
              { label: t("nav.boxes") },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("boxes.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("boxes.subtitle")}</p>
        </div>
        {canCreate && (
          <button
            onClick={() => setIsModalOpen(true)}
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            {t("boxes.create")}
          </button>
        )}
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {error}
        </div>
      )}

      {isLoading ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : boxes.length === 0 ? (
        <ResourceZeroState
          tone="violet"
          icon={<BoxesIcon className="h-8 w-8" />}
          title={t("boxes.empty.title")}
          description={t("boxes.empty.description")}
          cta={canCreate ? { label: t("boxes.create"), onClick: () => setIsModalOpen(true) } : undefined}
          steps={[t("boxes.empty.step1"), t("boxes.empty.step2"), t("boxes.empty.step3")]}
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {boxes.map((b) => (
            <div
              key={b.id}
              className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm"
            >
              <div className="mb-3 flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <p className="truncate font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{b.name}</p>
                  <p className="mt-0.5 truncate text-xs text-gray-400 dark:text-gray-500">
                    {b.image} · {b.profile}
                  </p>
                </div>
                <PhaseBadge phase={b.status} />
              </div>

              {b.expires_at && (
                <p className="mb-3 text-xs text-gray-500 dark:text-gray-400">
                  {t("boxes.col.expires")}: {timeUntil(b.expires_at) ?? t("boxes.expired")}
                </p>
              )}
              {b.error_message && (
                <p className="mb-3 line-clamp-2 text-xs text-red-600 dark:text-red-400">{b.error_message}</p>
              )}

              <div className="flex flex-wrap gap-2">
                {b.status === "Ready" && (
                  <button
                    onClick={() => openConnect(b.name, false)}
                    disabled={busyBox === b.name}
                    className="rounded-md border border-gray-200 dark:border-gray-700 px-2.5 py-1 text-xs font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50"
                  >
                    {t("boxes.action.connect")}
                  </button>
                )}
                {canCreate && b.status === "Ready" && (
                  <button
                    onClick={() => act(b.name, "suspend")}
                    disabled={busyBox === b.name}
                    className="rounded-md border border-gray-200 dark:border-gray-700 px-2.5 py-1 text-xs font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50"
                  >
                    {t("boxes.action.suspend")}
                  </button>
                )}
                {canCreate && b.status === "Sleeping" && (
                  <button
                    onClick={() => act(b.name, "resume")}
                    disabled={busyBox === b.name}
                    className="rounded-md border border-gray-200 dark:border-gray-700 px-2.5 py-1 text-xs font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50"
                  >
                    {t("boxes.action.resume")}
                  </button>
                )}
                {canCreate && (
                  <button
                    onClick={() => act(b.name, "remove")}
                    disabled={busyBox === b.name}
                    className="rounded-md border border-red-200 dark:border-red-900 px-2.5 py-1 text-xs font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/40 disabled:opacity-50"
                  >
                    {t("boxes.action.delete")}
                  </button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      <Modal isOpen={isModalOpen} onClose={() => !isSubmitting && setIsModalOpen(false)} title={t("boxes.modal.title")}>
        <form onSubmit={handleSubmit} className="space-y-4 px-4 py-4 sm:px-6">
          <div>
            <label htmlFor="box-name" className="block text-sm font-medium text-gray-700 dark:text-gray-300">
              {t("boxes.modal.name")}{" "}
              <span className="font-normal text-gray-400 dark:text-gray-500">{t("boxes.modal.nameHint")}</span>
            </label>
            <input
              id="box-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              pattern="[a-z0-9-]*"
              className="mt-1 w-full rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 font-mono text-sm text-gray-900 dark:text-gray-100 focus:border-blue-500 focus:outline-none"
            />
          </div>

          <div>
            <span className="block text-sm font-medium text-gray-700 dark:text-gray-300">{t("boxes.modal.ttl")}</span>
            <div className="mt-1 flex gap-2">
              {TTL_CHOICES.map((secs) => (
                <button
                  key={secs}
                  type="button"
                  onClick={() => setTtl(secs)}
                  className={`rounded-lg border px-3 py-1.5 text-sm transition-colors ${
                    ttl === secs
                      ? "border-blue-500 bg-blue-50 dark:bg-blue-950/40 text-blue-700 dark:text-blue-300"
                      : "border-gray-200 dark:border-gray-700 text-gray-600 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-800"
                  }`}
                >
                  {t(secs === 3600 ? "boxes.modal.ttl1h" : secs === 14400 ? "boxes.modal.ttl4h" : "boxes.modal.ttl12h")}
                </button>
              ))}
            </div>
            <p className="mt-1.5 text-xs text-gray-500 dark:text-gray-400">{t("boxes.modal.ttlNote")}</p>
          </div>

          {submitError && (
            <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-3 py-2 text-sm text-red-700 dark:text-red-300">
              {submitError}
            </div>
          )}

          {isSubmitting && <p className="text-xs text-gray-500 dark:text-gray-400">{t("boxes.creatingHint")}</p>}

          <button
            type="submit"
            disabled={isSubmitting}
            className="inline-flex w-full items-center justify-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {isSubmitting && <Spinner size="sm" />}
            {isSubmitting ? t("boxes.creating") : t("boxes.modal.submit")}
          </button>
        </form>
      </Modal>

      <Modal
        isOpen={connectBox !== null}
        onClose={() => {
          setConnectBox(null);
          setSession(null);
          setConnect(null);
        }}
        title={t("boxes.connect.title")}
      >
        <div className="space-y-4 overflow-y-auto px-4 py-4 sm:px-6">
          <p className="font-mono text-sm text-gray-900 dark:text-gray-100">{connectBox}</p>
          {readyMs && (
            <p className="text-xs text-gray-500 dark:text-gray-400">
              {t("boxes.connect.readyIn", { ms: readyMs.ms, pool: readyMs.pool })}
            </p>
          )}

          {session && (
            <div className="rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 p-3">
              <div className="mb-1.5 flex items-center justify-between gap-2">
                <span className="text-xs font-semibold text-amber-800 dark:text-amber-300">
                  {t("boxes.connect.token")}
                </span>
                <CopyButton value={session.token} label={t("boxes.connect.copy")} />
              </div>
              <pre className="overflow-x-auto whitespace-pre-wrap break-all font-mono text-xs text-amber-900 dark:text-amber-200">
                {session.token}
              </pre>
              <p className="mt-1.5 text-xs text-amber-700 dark:text-amber-400">{t("boxes.connect.tokenWarning")}</p>
            </div>
          )}

          {connect?.ssh_command && (
            <div>
              <div className="mb-1.5 flex items-center justify-between gap-2">
                <span className="text-xs font-semibold text-gray-700 dark:text-gray-300">{t("boxes.connect.ssh")}</span>
                <CopyButton value={connect.ssh_command} label={t("boxes.connect.copy")} />
              </div>
              <pre className="overflow-x-auto rounded-lg bg-gray-50 dark:bg-gray-800 p-3 font-mono text-xs text-gray-800 dark:text-gray-200">
                {connect.ssh_command}
              </pre>
            </div>
          )}

          {connect?.mcp && (
            <div>
              <div className="mb-1.5 flex items-center justify-between gap-2">
                <span className="text-xs font-semibold text-gray-700 dark:text-gray-300">{t("boxes.connect.mcp")}</span>
                <CopyButton value={connect.mcp.snippet} label={t("boxes.connect.copy")} />
              </div>
              <pre className="overflow-x-auto rounded-lg bg-gray-50 dark:bg-gray-800 p-3 font-mono text-xs text-gray-800 dark:text-gray-200">
                {connect.mcp.snippet}
              </pre>
              {!connect.mcp.available && (
                <p className="mt-1.5 text-xs text-red-600 dark:text-red-400">{t("boxes.connect.mcpUnavailable")}</p>
              )}
            </div>
          )}

          {connectBox && !session && (
            <button
              type="button"
              onClick={() => openConnect(connectBox, true)}
              disabled={busyBox === connectBox}
              className="rounded-md border border-gray-200 dark:border-gray-700 px-2.5 py-1 text-xs font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50"
            >
              {t("boxes.connect.newSession")}
            </button>
          )}
        </div>
      </Modal>
    </div>
  );
}
