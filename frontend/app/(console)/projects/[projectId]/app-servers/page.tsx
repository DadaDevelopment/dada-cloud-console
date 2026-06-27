"use client";
import { FormEvent, useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { appServersApi } from "@/lib/api";
import type { AppServer, AppServerStatus } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Modal } from "@/components/ui/modal";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { DataTable, type Column } from "@/components/ui/data-table";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";

type AppServerMode = "terraform" | "manual";

interface CreateAppServerForm {
  name: string;
  mode: AppServerMode;
  // terraform
  flavor: string;
  os_image: string;
  region: string;
  ssh_key_name: string;
  // manual
  vm_ip: string;
  ssh_user: string;
  ssh_port: string;
  ssh_private_key: string;
}

const emptyForm: CreateAppServerForm = {
  name: "",
  mode: "terraform",
  flavor: "small",
  os_image: "ubuntu-22.04",
  region: "ru1",
  ssh_key_name: "dada-agent",
  vm_ip: "",
  ssh_user: "root",
  ssh_port: "22",
  ssh_private_key: "",
};

const statusTone: Record<AppServerStatus, string> = {
  Provisioning: "bg-blue-100 text-blue-800",
  WaitingForAgent: "bg-amber-100 text-amber-800",
  Ready: "bg-green-100 text-green-800",
  Deleting: "bg-orange-100 text-orange-800",
  Deleted: "bg-gray-200 text-gray-700",
  Failed: "bg-red-100 text-red-800",
};

function AppServerStatusBadge({ status }: { status: AppServerStatus }) {
  return <Badge className={statusTone[status] ?? "bg-gray-100 text-gray-800"}>{status}</Badge>;
}

export default function AppServersPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const router = useRouter();
  const { t } = useT();
  const { project, role } = useProjectContext();
  const canManage = canMutate(role);

  const [servers, setServers] = useState<AppServer[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [deletingName, setDeletingName] = useState<string | null>(null);
  const [online, setOnline] = useState<Record<string, boolean>>({});
  const [form, setForm] = useState<CreateAppServerForm>(emptyForm);

  async function loadServers() {
    setError(null);
    try {
      const data = await appServersApi.list(projectId);
      const list = data.app_servers ?? [];
      setServers(list);
      // Best-effort live online state (Portainer heartbeat) per Ready server.
      void Promise.all(
        list
          .filter((s) => s.status === "Ready")
          .map((s) =>
            appServersApi
              .getState(projectId, s.name)
              .then((st) => [s.name, st.online] as const)
              .catch(() => [s.name, false] as const)
          )
      ).then((pairs) => setOnline(Object.fromEntries(pairs)));
    } catch (err) {
      setError(err instanceof Error ? err.message : t("appServers.error.load"));
    } finally {
      setIsLoading(false);
    }
  }

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fetch-on-mount pattern used by console pages; state updates happen after the API promise settles.
    void loadServers();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- loadServers closes over stable projectId for this fetch-on-mount pattern.
  }, [projectId]);

  function handleFormChange(field: Exclude<keyof CreateAppServerForm, "mode">, value: string) {
    setForm((prev) => ({ ...prev, [field]: value }));
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setIsSubmitting(true);
    try {
      const payload =
        form.mode === "manual"
          ? {
              name: form.name,
              mode: "manual" as const,
              vm_ip: form.vm_ip.trim(),
              ssh_user: form.ssh_user.trim() || "root",
              ssh_port: Number(form.ssh_port) || 22,
              ssh_private_key: form.ssh_private_key,
            }
          : {
              name: form.name,
              mode: "terraform" as const,
              flavor: form.flavor,
              os_image: form.os_image,
              region: form.region,
              ssh_key_name: form.ssh_key_name,
            };
      const result = await appServersApi.create(projectId, payload);
      setIsModalOpen(false);
      setForm(emptyForm);
      const opId = result.operation?.id;
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : t("appServers.error.create"));
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleDelete(serverName: string) {
    setDeletingName(serverName);
    setError(null);
    try {
      const result = await appServersApi.remove(projectId, serverName);
      const opId = result.operation?.id;
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("appServers.error.delete"));
    } finally {
      setDeletingName(null);
    }
  }

  const columns: Column<AppServer>[] = [
    {
      key: "name",
      header: t("appServers.col.name"),
      sortValue: (s) => s.name,
      render: (s) => (
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <Link
              href={`/projects/${projectId}/app-servers/${s.name}`}
              className="font-mono text-sm font-semibold text-gray-900 hover:text-amber-700 hover:underline"
            >
              {s.name}
            </Link>
            {s.source === "manual" && (
              <span className="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-gray-500">manual</span>
            )}
          </div>
          <p className="mt-0.5 text-xs text-gray-400">{t("appServers.updated", { ago: timeAgo(s.updated_at) })}</p>
          {s.error_message && <p className="mt-1 text-xs text-red-600">{s.error_message}</p>}
        </div>
      ),
    },
    {
      key: "status",
      header: t("appServers.col.status"),
      sortValue: (s) => s.status,
      render: (s) => (
        <div className="flex items-center gap-2">
          <AppServerStatusBadge status={s.status} />
          {s.status === "Ready" && (
            <span
              title={online[s.name] ? t("appServers.heartbeat.online") : t("appServers.heartbeat.none")}
              className={`inline-block h-2 w-2 rounded-full ${online[s.name] ? "bg-green-400" : "bg-gray-300"}`}
            />
          )}
        </div>
      ),
    },
    { key: "ip", header: t("appServers.col.vmIp"), render: (s) => <span className="font-mono text-gray-600">{s.vm_ip ?? "—"}</span> },
    { key: "portainer", header: t("appServers.col.portainer"), render: (s) => <span className="font-mono text-gray-600">{s.portainer_endpoint_id ?? "—"}</span> },
    ...(canManage
      ? [{
          key: "actions",
          header: t("appServers.col.actions"),
          align: "right" as const,
          render: (s: AppServer) => (
            <button
              onClick={() => void handleDelete(s.name)}
              disabled={deletingName === s.name || s.status === "Deleting"}
              className="rounded-lg border border-red-200 px-3 py-1.5 text-sm font-medium text-red-700 hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {deletingName === s.name ? t("common.deleting") : t("common.delete")}
            </button>
          ),
        }]
      : []),
  ];

  return (
    <div>
      <div className="mb-8 flex items-start justify-between">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.projects"), href: "/projects" },
              { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
              { label: t("nav.app-servers") },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900">{t("appServers.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500">{t("appServers.subtitle")}</p>
        </div>
        {canManage && (
        <button
          onClick={() => setIsModalOpen(true)}
          className="inline-flex items-center gap-2 rounded-lg bg-amber-600 px-4 py-2 text-sm font-medium text-white hover:bg-amber-700 transition-colors"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          {t("appServers.create")}
        </button>
        )}
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      )}

      <DataTable<AppServer>
        loading={isLoading}
        rows={servers}
        getRowKey={(s) => s.id}
        searchText={(s) => `${s.name} ${s.vm_ip ?? ""} ${s.status}`}
        searchPlaceholder={t("appServers.search")}
        columns={columns}
        emptyState={
          <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-16">
            <svg className="mb-3 h-12 w-12 text-gray-300" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M3 15.75V8.25A2.25 2.25 0 015.25 6h13.5A2.25 2.25 0 0121 8.25v7.5A2.25 2.25 0 0118.75 18H5.25A2.25 2.25 0 013 15.75zM7 9h10M7 12h4" />
            </svg>
            <p className="text-sm font-medium text-gray-500">{t("appServers.empty.title")}</p>
            {canManage && (
              <button onClick={() => setIsModalOpen(true)} className="mt-4 text-sm text-amber-700 hover:text-amber-800">
                {t("appServers.empty.provision")}
              </button>
            )}
          </div>
        }
      />

      <Modal
        isOpen={isModalOpen}
        onClose={() => {
          setIsModalOpen(false);
          setSubmitError(null);
        }}
        title={t("appServers.modal.title")}
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">{t("appServers.field.name.label")}</label>
            <input
              type="text"
              required
              value={form.name}
              onChange={(e) => handleFormChange("name", e.target.value)}
              placeholder="client-a-prod-1"
              className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
            />
            <p className="mt-1 text-xs text-gray-400">{t("appServers.field.name.help")}</p>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">{t("appServers.field.source.label")}</label>
            <div className="mt-1 inline-flex rounded-lg border border-gray-300 p-0.5">
              {(["terraform", "manual"] as AppServerMode[]).map((m) => (
                <button
                  key={m}
                  type="button"
                  onClick={() => setForm((prev) => ({ ...prev, mode: m }))}
                  className={`rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
                    form.mode === m ? "bg-amber-600 text-white" : "text-gray-600 hover:bg-gray-100"
                  }`}
                >
                  {m === "terraform" ? t("appServers.field.source.terraform") : t("appServers.field.source.manual")}
                </button>
              ))}
            </div>
            <p className="mt-1 text-xs text-gray-400">
              {form.mode === "terraform"
                ? t("appServers.field.source.help.terraform")
                : t("appServers.field.source.help.manual")}
            </p>
          </div>

          {form.mode === "terraform" && (
          <>
          <div className="grid gap-4 sm:grid-cols-2">
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("appServers.field.flavor.label")}</label>
              <select
                value={form.flavor}
                onChange={(e) => handleFormChange("flavor", e.target.value)}
                className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
              >
                <option value="small">small</option>
                <option value="medium">medium</option>
                <option value="large">large</option>
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("appServers.field.region.label")}</label>
              <select
                value={form.region}
                onChange={(e) => handleFormChange("region", e.target.value)}
                className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
              >
                <option value="ru1">ru1</option>
                <option value="ru2">ru2</option>
                <option value="kz1">kz1</option>
                <option value="eu1">eu1</option>
              </select>
            </div>
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("appServers.field.osImage.label")}</label>
              <input
                type="text"
                value={form.os_image}
                onChange={(e) => handleFormChange("os_image", e.target.value)}
                className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("appServers.field.sshKeyName.label")}</label>
              <input
                type="text"
                value={form.ssh_key_name}
                onChange={(e) => handleFormChange("ssh_key_name", e.target.value)}
                className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
              />
            </div>
          </div>
          </>
          )}

          {form.mode === "manual" && (
          <>
          <div className="grid gap-4 sm:grid-cols-[2fr_1fr]">
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("appServers.field.vmIp.label")}</label>
              <input
                type="text"
                required
                value={form.vm_ip}
                onChange={(e) => handleFormChange("vm_ip", e.target.value)}
                placeholder="203.0.113.10"
                className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("appServers.field.sshPort.label")}</label>
              <input
                type="number"
                value={form.ssh_port}
                onChange={(e) => handleFormChange("ssh_port", e.target.value)}
                placeholder="22"
                className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
              />
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">{t("appServers.field.sshUser.label")}</label>
            <input
              type="text"
              value={form.ssh_user}
              onChange={(e) => handleFormChange("ssh_user", e.target.value)}
              placeholder="root"
              className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">{t("appServers.field.sshKey.label")}</label>
            <textarea
              required
              value={form.ssh_private_key}
              onChange={(e) => handleFormChange("ssh_private_key", e.target.value)}
              placeholder={"-----BEGIN OPENSSH PRIVATE KEY-----\n..."}
              rows={6}
              className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 font-mono text-xs focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
            />
            <p className="mt-1 text-xs text-amber-700">
              {t("appServers.field.sshKey.warn")}
            </p>
          </div>
          </>
          )}

          {submitError && (
            <div role="alert" className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
              {submitError}
            </div>
          )}

          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => setIsModalOpen(false)}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100"
            >
              {t("common.cancel")}
            </button>
            <button
              type="submit"
              disabled={isSubmitting}
              className="rounded-lg bg-amber-600 px-4 py-2 text-sm font-medium text-white hover:bg-amber-700 disabled:opacity-50"
            >
              {isSubmitting ? t("common.creating") : t("appServers.create")}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
