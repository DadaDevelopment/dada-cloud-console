"use client";
import { useState } from "react";
import { useRouter } from "next/navigation";
import { gitApi, buildsApi, classifyConnectRepoConflict } from "@/lib/api";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";
import { parseGitCloneUrl, type ParseGitCloneUrlError } from "@/lib/git-clone-url";
import { trackBuildStart } from "@/lib/build-watch";

function toKubeName(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "")
    .slice(0, 63);
}

function deriveAppNameFromRepo(repoFullName: string): string {
  return toKubeName(repoFullName.split("/").pop() || repoFullName);
}

export interface ConnectByUrlDialogProps {
  projectId: string;
  envId: string | null;
  open: boolean;
  onClose: () => void;
}

/**
 * Connects a repository the backend already supports (any https git host,
 * with an optional token) without going through the GitHub App install flow.
 * ConnectGitRepo (backend/internal/api/gitrepos.go) already accepts
 * provider/clone_url/token; this form is what lets a user reach it - GitLab
 * (including self-hosted), Gitea, Bitbucket, or any other https remote.
 *
 * Kept as a standalone submit-and-redirect flow, mirroring
 * components/deploy/upload-deploy.tsx: link the repo, trigger the first
 * build, and land on that build's log page. It does not join the multi-step
 * GitHub wizard on the import page because there is no repo picker or
 * framework-detection step to share - the user already knows their URL.
 */
export function ConnectByUrlDialog({ projectId, envId, open, onClose }: ConnectByUrlDialogProps) {
  const { t } = useT();
  const router = useRouter();

  const [cloneUrl, setCloneUrl] = useState("");
  const [appName, setAppName] = useState("");
  const [appNameTouched, setAppNameTouched] = useState(false);
  const [token, setToken] = useState("");
  const [branch, setBranch] = useState("main");
  const [rootDir, setRootDir] = useState(".");
  const [port, setPort] = useState(8080);
  const [worker, setWorker] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function cloneUrlErrorMessage(code: ParseGitCloneUrlError): string {
    switch (code) {
      case "empty":
        return t("git.import.byUrl.error.empty");
      case "ssh-not-supported":
        return t("git.import.byUrl.error.sshNotSupported");
      case "http-not-supported":
        return t("git.import.byUrl.error.httpNotSupported");
      case "incomplete-path":
        return t("git.import.byUrl.error.incompletePath");
      default:
        return t("git.import.byUrl.error.invalidUrl");
    }
  }

  function onCloneUrlChange(value: string) {
    setCloneUrl(value);
    if (appNameTouched) return;
    const parsed = parseGitCloneUrl(value);
    if (parsed.ok) setAppName(deriveAppNameFromRepo(parsed.value.repoFullName));
  }

  function reset() {
    setCloneUrl("");
    setAppName("");
    setAppNameTouched(false);
    setToken("");
    setBranch("main");
    setRootDir(".");
    setPort(8080);
    setWorker(false);
    setError(null);
  }

  async function handleSubmit() {
    if (!envId) return;
    const parsed = parseGitCloneUrl(cloneUrl);
    if (!parsed.ok) {
      setError(cloneUrlErrorMessage(parsed.error));
      return;
    }
    const trimmedName = appName.trim();
    if (!trimmedName) return;

    setSubmitting(true);
    setError(null);
    try {
      await gitApi.linkRepo(projectId, envId, {
        repo_full_name: parsed.value.repoFullName,
        provider: parsed.value.provider,
        clone_url: parsed.value.cloneUrl,
        app_name: trimmedName,
        production_branch: branch || "main",
        root_dir: rootDir || ".",
        auto_deploy: true,
        port: worker ? 0 : port,
        worker,
        token: token || undefined,
      });
    } catch (err) {
      const apiErr = err as { status?: number; code?: string } | undefined;
      const conflict = classifyConnectRepoConflict(apiErr?.status, apiErr?.code);
      const msg = err instanceof Error ? err.message : t("git.import.error.connect");
      if (conflict === "repo_already_connected") {
        setError(t("git.import.byUrl.error.repoAlreadyConnected"));
      } else if (conflict === "app_name_taken") {
        setError(t("git.import.byUrl.error.appNameTaken"));
      } else {
        setError(msg);
      }
      setSubmitting(false);
      return;
    }

    try {
      const { build } = await buildsApi.trigger(projectId, envId, trimmedName);
      if (build?.id) {
        trackBuildStart({ projectId, envId, appName: trimmedName, buildId: build.id });
      }
      router.push(`/projects/${projectId}/apps/${trimmedName}/builds/${build.id}?envId=${envId}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("git.import.deploy.triggerFailed"));
      setSubmitting(false);
      return;
    }
    reset();
  }

  const canSubmit = cloneUrl.trim() !== "" && appName.trim() !== "" && !!envId && !submitting;

  return (
    <Modal
      isOpen={open}
      onClose={() => {
        if (!submitting) onClose();
      }}
      title={t("git.import.byUrl.title")}
    >
      <div className="space-y-4">
        <p className="text-sm text-gray-500 dark:text-gray-400">{t("git.import.byUrl.subtitle")}</p>

        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
            {t("git.import.byUrl.cloneUrl.label")}
          </label>
          <input
            type="text"
            value={cloneUrl}
            onChange={(e) => onCloneUrlChange(e.target.value)}
            placeholder={t("git.import.byUrl.cloneUrl.placeholder")}
            className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
          <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("git.import.byUrl.cloneUrl.hint")}</p>
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
            {t("git.import.byUrl.token.label")}
          </label>
          <input
            type="password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            autoComplete="off"
            className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
          <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("git.import.byUrl.token.hint")}</p>
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
            {t("git.import.byUrl.appName.label")}
          </label>
          <input
            type="text"
            required
            value={appName}
            onChange={(e) => {
              setAppNameTouched(true);
              setAppName(toKubeName(e.target.value));
            }}
            pattern="[a-z0-9-]+"
            className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {t("git.import.byUrl.branch.label")}
            </label>
            <input
              type="text"
              value={branch}
              onChange={(e) => setBranch(e.target.value)}
              placeholder="main"
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {t("git.import.byUrl.rootDir.label")}
            </label>
            <input
              type="text"
              value={rootDir}
              onChange={(e) => setRootDir(e.target.value)}
              placeholder="."
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
            {t("git.import.byUrl.port.label")}
          </label>
          <input
            type="number"
            required={!worker}
            disabled={worker}
            min={1}
            max={65535}
            value={worker ? "" : port}
            onChange={(e) => setPort(parseInt(e.target.value, 10) || 8080)}
            className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 disabled:cursor-not-allowed disabled:bg-gray-50 dark:disabled:bg-gray-900"
          />
        </div>

        <label className="flex items-start gap-3 rounded-lg border border-gray-200 dark:border-gray-800 px-4 py-3">
          <input
            type="checkbox"
            checked={worker}
            onChange={(e) => setWorker(e.target.checked)}
            className="mt-0.5 h-4 w-4 rounded border-gray-300 dark:border-gray-700"
          />
          <span className="text-sm font-medium text-gray-700 dark:text-gray-200">{t("git.import.byUrl.worker.label")}</span>
        </label>

        {error && (
          <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-3 py-2 text-sm text-red-700 dark:text-red-300">
            {error}
          </div>
        )}

        <div className="flex justify-end gap-2 pt-1">
          <button
            type="button"
            onClick={onClose}
            disabled={submitting}
            className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:cursor-not-allowed disabled:opacity-60"
          >
            {t("git.import.byUrl.cancel")}
          </button>
          <button
            type="button"
            data-ux="git_import:connect_url_submit"
            onClick={handleSubmit}
            disabled={!canSubmit}
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
          >
            {submitting && <Spinner size="sm" />}
            {submitting ? t("git.import.byUrl.submitting") : t("git.import.byUrl.submit")}
          </button>
        </div>
      </div>
    </Modal>
  );
}
