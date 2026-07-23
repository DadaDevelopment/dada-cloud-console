"use client";
import { useEffect, useState } from "react";
import { appsApi } from "@/lib/api";
import { useT } from "@/lib/i18n/console/context";
import { Spinner } from "@/components/ui/spinner";

interface Props {
  projectId: string;
  envId: string;
  appName: string;
  canEdit: boolean;
}

interface ComposeDesired {
  image?: string;
  ports?: string[];
  compose?: { image?: string; ports?: string[] };
}

const PORT_RE = /^(\d{1,3}(\.\d{1,3}){3}:)?\d{1,5}(-\d{1,5})?(:\d{1,5}(-\d{1,5})?)?(\/(tcp|udp|sctp))?$/;

/**
 * ComposeConfigEditor edits the image and published ports of a VM (docker
 * compose) app. Unlike CommonConfigEditor (Helm values.yaml over a delegated
 * WebSocket), this reads/writes the app's compose desired-state fields via a
 * plain REST round trip: appsApi.list to load, appsApi.updateComposeConfig to
 * save. Saving redeploys the compose stack on the VM.
 */
export function ComposeConfigEditor({ projectId, envId, appName, canEdit }: Props) {
  const { t } = useT();
  const [image, setImage] = useState("");
  const [ports, setPorts] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  useEffect(() => {
    if (!envId) return;
    appsApi
      .list(projectId, envId)
      .then((d) => {
        const app = (d.apps ?? []).find((a) => a.name === appName);
        const desired = app?.summary_json?.desired as ComposeDesired | undefined;
        const compose = desired?.compose;
        setImage(compose?.image ?? desired?.image ?? "");
        setPorts(compose?.ports ?? desired?.ports ?? []);
      })
      .catch(() => {});
  }, [projectId, envId, appName]);

  function updatePort(i: number, v: string) {
    setPorts((p) => p.map((x, idx) => (idx === i ? v : x)));
  }
  function removePort(i: number) {
    setPorts((p) => p.filter((_, idx) => idx !== i));
  }
  function addPort() {
    setPorts((p) => [...p, ""]);
  }

  function validate(): string | null {
    if (!image.trim()) return t("apps.compose.config.invalid.image");
    for (const p of ports) {
      const v = p.trim();
      if (v && !PORT_RE.test(v)) return t("apps.compose.config.invalid.port");
    }
    return null;
  }

  async function save() {
    const err = validate();
    if (err) {
      setMsg({ kind: "err", text: err });
      return;
    }
    setSaving(true);
    setMsg(null);
    try {
      const cleanPorts = ports.map((p) => p.trim()).filter(Boolean);
      await appsApi.updateComposeConfig(projectId, envId, appName, { image: image.trim(), ports: cleanPorts });
      setPorts(cleanPorts);
      setMsg({ kind: "ok", text: t("apps.compose.config.queued") });
    } catch (e) {
      setMsg({ kind: "err", text: e instanceof Error ? e.message : t("apps.compose.config.error") });
    } finally {
      setSaving(false);
    }
  }

  const field =
    "mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 disabled:opacity-50";
  const rowField =
    "w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 disabled:opacity-50";
  const label = "block text-sm font-medium text-gray-700 dark:text-gray-300";
  const disabled = !canEdit || saving;

  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-6">
      <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("apps.compose.config.title")}</h2>
      <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("apps.compose.config.subtitle")}</p>

      <div className="mt-5">
        <label className={label}>{t("apps.config.imageName")}</label>
        <input
          className={field}
          value={image}
          onChange={(e) => setImage(e.target.value)}
          disabled={disabled}
          placeholder="nginx:1.27"
        />
        <p className="mt-1 text-xs text-gray-400">{t("apps.compose.config.imageHint")}</p>
      </div>

      <div className="mt-5">
        <label className={label}>{t("apps.compose.config.ports.label")}</label>
        <p className="mt-1 text-xs text-gray-400">{t("apps.compose.config.ports.hint")}</p>
        <div className="mt-2 space-y-2">
          {ports.map((p, i) => (
            <div key={i} className="flex items-center gap-2">
              <input
                className={rowField}
                value={p}
                onChange={(e) => updatePort(i, e.target.value)}
                disabled={disabled}
                placeholder="8080:80"
              />
              <button
                type="button"
                onClick={() => removePort(i)}
                disabled={disabled}
                aria-label={t("common.remove")}
                className="shrink-0 rounded-lg border border-gray-300 dark:border-gray-700 p-2 text-gray-500 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50"
              >
                <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>
          ))}
        </div>
        <button
          type="button"
          onClick={addPort}
          disabled={disabled}
          className="mt-2 inline-flex items-center gap-1.5 rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50"
        >
          <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          {t("common.add")}
        </button>
      </div>

      {msg && (
        <p
          className={`mt-4 text-sm ${
            msg.kind === "ok" ? "text-green-600 dark:text-green-400" : "text-red-600 dark:text-red-400"
          }`}
        >
          {msg.text}
        </p>
      )}

      <button
        onClick={save}
        disabled={disabled}
        className="mt-4 inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
      >
        {saving ? (
          <>
            <Spinner size="sm" />
            {t("apps.config.saving")}
          </>
        ) : (
          t("apps.config.save")
        )}
      </button>
    </div>
  );
}
