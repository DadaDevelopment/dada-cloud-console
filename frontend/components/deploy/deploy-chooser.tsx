"use client";
import { useState } from "react";
import { useRouter } from "next/navigation";
import { GitBranch, Package, Boxes } from "lucide-react";
import { Modal } from "@/components/ui/modal";
import { Button } from "@/components/ui/button";
import type { Environment } from "@/lib/types";
import { useT } from "@/lib/i18n/console/context";

type DeployKind = "git" | "image" | "compose";

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
  onPickImage,
}: {
  open: boolean;
  onClose: () => void;
  projectId: string;
  environments: Environment[];
  defaultEnvId: string;
  onPickImage: (envId: string) => void;
}) {
  const { t } = useT();
  const router = useRouter();
  const [envId, setEnvId] = useState(defaultEnvId);
  const [kind, setKind] = useState<DeployKind>("git");

  // Re-sync the selected env whenever the wizard is reopened from a different block.
  const [seenDefault, setSeenDefault] = useState(defaultEnvId);
  if (open && defaultEnvId !== seenDefault) {
    setSeenDefault(defaultEnvId);
    setEnvId(defaultEnvId);
  }

  function proceed() {
    onClose();
    if (kind === "git") {
      router.push(`/projects/${projectId}/git/import?envId=${envId}`);
    } else if (kind === "compose") {
      router.push(`/projects/${projectId}/app-servers`);
    } else {
      onPickImage(envId);
    }
  }

  const cards: { key: DeployKind; icon: React.ReactNode; title: string; desc: string }[] = [
    { key: "git", icon: <GitBranch className="h-5 w-5" />, title: t("apps.deploy.fromGit.title"), desc: t("apps.deploy.fromGit.desc") },
    { key: "image", icon: <Package className="h-5 w-5" />, title: t("apps.deploy.fromImage.title"), desc: t("apps.deploy.fromImage.desc") },
    { key: "compose", icon: <Boxes className="h-5 w-5" />, title: t("apps.deploy.fromCompose.title"), desc: t("apps.deploy.fromCompose.desc") },
  ];

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
          <div className="grid gap-3 sm:grid-cols-3">
            {cards.map((c) => {
              const active = kind === c.key;
              return (
                <button
                  key={c.key}
                  type="button"
                  onClick={() => setKind(c.key)}
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

        <div className="flex justify-end gap-3 pt-1">
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={proceed} disabled={kind !== "compose" && !envId}>
            {t("apps.deploy.continue")}
          </Button>
        </div>
      </div>
    </Modal>
  );
}
