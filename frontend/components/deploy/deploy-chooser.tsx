"use client";
import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { GitBranch, Package, Boxes, UploadCloud } from "lucide-react";
import { Modal } from "@/components/ui/modal";
import { Button } from "@/components/ui/button";
import { UploadDeployCard } from "@/components/deploy/upload-deploy";
import type { Environment } from "@/lib/types";
import { useT } from "@/lib/i18n/console/context";
import { trackUxEvent } from "@/lib/ux-telemetry";

type DeployKind = "git" | "image" | "compose" | "upload";

/**
 * Unified deploy entry point: one wizard step that picks the target environment
 * and the deployment source, then hands off to the matching flow — the Git import
 * wizard, the image-create form (via onPickImage), or the App Servers area where a
 * docker-compose stack is connected and adopted. Replaces the ambiguous single
 * "Deploy" button with three explicit paths.
 */
export function DeployChooser({
  open,
  onClose,
  projectId,
  environments,
  defaultEnvId,
  hasGitSourceApp,
  onPickImage,
}: {
  open: boolean;
  onClose: () => void;
  projectId: string;
  environments: Environment[];
  defaultEnvId: string;
  /** True once the project has at least one app deployed from a connected git repo. */
  hasGitSourceApp: boolean;
  onPickImage: (envId: string) => void;
}) {
  const { t } = useT();
  const router = useRouter();
  const defaultKind: DeployKind = hasGitSourceApp ? "git" : "upload";
  const [envId, setEnvId] = useState(defaultEnvId);
  const [kind, setKind] = useState<DeployKind>(defaultKind);

  /**
   * Re-sync the selected env and the default source card whenever the wizard
   * is reopened for a different block, or the project's git-source signal
   * changed since the last time it was open (e.g. the first app just landed).
   */
  const [seenDefault, setSeenDefault] = useState(defaultEnvId);
  const [seenHasGitSourceApp, setSeenHasGitSourceApp] = useState(hasGitSourceApp);
  if (open && (defaultEnvId !== seenDefault || hasGitSourceApp !== seenHasGitSourceApp)) {
    setSeenDefault(defaultEnvId);
    setSeenHasGitSourceApp(hasGitSourceApp);
    setEnvId(defaultEnvId);
    setKind(defaultKind);
  }

  useEffect(() => {
    if (open) trackUxEvent("view", "create_app_modal:opened");
  }, [open]);

  const openedDefaultRef = useRef<DeployKind | null>(null);
  useEffect(() => {
    if (!open) {
      openedDefaultRef.current = null;
      return;
    }
    if (openedDefaultRef.current === defaultKind) return;
    openedDefaultRef.current = defaultKind;
    trackUxEvent("view", `apps_deploy_chooser:opened_default_${defaultKind}`);
  }, [open, defaultKind]);

  function proceed() {
    onClose();
    if (kind === "git") {
      router.push(`/projects/${projectId}/git/import?envId=${envId}`);
    } else if (kind === "compose") {
      router.push(`/projects/${projectId}/app-servers`);
    } else if (kind === "image") {
      onPickImage(envId);
    }
  }

  const gitCard = { key: "git" as const, icon: <GitBranch className="h-5 w-5" />, title: t("apps.deploy.fromGit.title"), desc: t("apps.deploy.fromGit.desc") };
  const imageCard = { key: "image" as const, icon: <Package className="h-5 w-5" />, title: t("apps.deploy.fromImage.title"), desc: t("apps.deploy.fromImage.desc") };
  const uploadCard = { key: "upload" as const, icon: <UploadCloud className="h-5 w-5" />, title: t("apps.deploy.fromUpload.title"), desc: t("apps.deploy.fromUpload.desc") };
  const composeCard = { key: "compose" as const, icon: <Boxes className="h-5 w-5" />, title: t("apps.deploy.fromCompose.title"), desc: t("apps.deploy.fromCompose.desc") };

  const cards: { key: DeployKind; icon: React.ReactNode; title: string; desc: string }[] = hasGitSourceApp
    ? [gitCard, imageCard, uploadCard, composeCard]
    : [uploadCard, gitCard, imageCard, composeCard];

  return (
    <Modal isOpen={open} onClose={onClose} title={t("apps.deploy.title")}>
      <div className="space-y-6">
        {kind !== "compose" && (
          <div>
            <label className="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.deploy.chooseEnv")}</label>
            <select
              value={envId}
              onChange={(e) => setEnvId(e.target.value)}
              className="w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2.5 text-sm text-gray-900 dark:text-gray-100 focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/30"
            >
              {environments.map((env) => (
                <option key={env.id} value={env.id}>
                  {env.name} · {env.runtime === "vm" ? "VM" : "Cloud"}
                </option>
              ))}
            </select>
          </div>
        )}

        <div>
          <span className="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.deploy.chooseSource")}</span>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            {cards.map((c) => {
              const active = kind === c.key;
              return (
                <button
                  key={c.key}
                  type="button"
                  onClick={() => setKind(c.key)}
                  data-ux={`apps_deploy_chooser:pick_${c.key}`}
                  className={`flex flex-col gap-2 rounded-xl border p-4 text-left transition-colors ${
                    active
                      ? "border-blue-500 bg-blue-50 dark:bg-blue-950/40 ring-2 ring-blue-500/30"
                      : "border-gray-300 dark:border-gray-700 hover:border-gray-400 dark:hover:border-gray-600"
                  }`}
                >
                  <span className={`flex h-9 w-9 items-center justify-center rounded-lg ${active ? "bg-blue-600 text-white" : "bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-300"}`}>
                    {c.icon}
                  </span>
                  <span className="text-sm font-semibold text-gray-900 dark:text-gray-100">{c.title}</span>
                  <span className="text-xs leading-relaxed text-gray-500 dark:text-gray-400">{c.desc}</span>
                </button>
              );
            })}
          </div>
        </div>

        {kind === "upload" ? (
          <UploadDeployCard projectId={projectId} envId={envId || null} compact />
        ) : (
          <div className="flex justify-end gap-3 pt-1">
            <Button variant="ghost" onClick={onClose} data-ux="apps_deploy_chooser:cancel">
              {t("common.cancel")}
            </Button>
            <Button onClick={proceed} disabled={kind !== "compose" && !envId} data-ux={`apps_deploy_chooser:continue_${kind}`}>
              {t("apps.deploy.continue")}
            </Button>
          </div>
        )}
      </div>
    </Modal>
  );
}
