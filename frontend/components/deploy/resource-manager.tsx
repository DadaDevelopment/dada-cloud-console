"use client";
import { useEffect, useState } from "react";
import { appsApi } from "@/lib/api";
import { useT } from "@/lib/i18n/console/context";

const PROFILES: { value: string; label: string }[] = [
  { value: "small", label: "small" },
  { value: "medium", label: "medium" },
  { value: "large", label: "large" },
];

interface Props {
  projectId: string;
  envId: string;
  appName: string;
  canEdit: boolean;
}

export function ResourceManager({ projectId, envId, appName, canEdit }: Props) {
  const { t } = useT();
  const [current, setCurrent] = useState<string | null>(null);
  const [profile, setProfile] = useState(PROFILES[0].value);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  useEffect(() => {
    if (!envId) return;
    appsApi
      .list(projectId, envId)
      .then((d) => {
        const app = (d.apps ?? []).find((a) => a.name === appName);
        const p = app?.summary_json?.profile as string | undefined;
        if (p) {
          setCurrent(p);
          setProfile(p);
        }
      })
      .catch(() => {});
  }, [projectId, envId, appName]);

  async function submit() {
    const previous = current;
    setBusy(true);
    setMsg(null);
    setCurrent(profile);
    try {
      await appsApi.updateProfile(projectId, envId, appName, profile);
      setMsg({ kind: "ok", text: t("apps.resources.queued") });
    } catch (e) {
      setCurrent(previous);
      setMsg({ kind: "err", text: e instanceof Error ? e.message : t("apps.resources.error") });
    } finally {
      setBusy(false);
    }
  }

  const field =
    "mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 disabled:opacity-50";
  const label = "block text-sm font-medium text-gray-700 dark:text-gray-300";

  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-6">
      <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("apps.resources.title")}</h2>
      <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("apps.resources.subtitle")}</p>

      <div className="mt-4 rounded-lg bg-gray-50 dark:bg-gray-950 px-4 py-3 text-sm">
        <span className="text-gray-500 dark:text-gray-400">{t("apps.resources.current")}: </span>
        {current ? (
          <span className="font-mono text-gray-900 dark:text-gray-100">{current}</span>
        ) : (
          <span className="text-gray-400 dark:text-gray-500">{t("apps.resources.none")}</span>
        )}
      </div>

      <div className="mt-5">
        <label className={label}>{t("apps.resources.plan")}</label>
        <select
          className={field}
          value={profile}
          onChange={(e) => setProfile(e.target.value)}
          disabled={!canEdit || busy}
        >
          {PROFILES.map((p) => (
            <option key={p.value} value={p.value}>
              {p.label}
            </option>
          ))}
        </select>
      </div>

      <p className="mt-3 text-xs text-amber-600 dark:text-amber-500">{t("apps.resources.warnRestart")}</p>

      {msg && (
        <p
          className={`mt-3 text-sm ${
            msg.kind === "ok" ? "text-green-600 dark:text-green-400" : "text-red-600 dark:text-red-400"
          }`}
        >
          {msg.text}
        </p>
      )}

      <button
        onClick={submit}
        disabled={!canEdit || busy || profile === current}
        className="mt-4 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
      >
        {t("apps.resources.save")}
      </button>
    </div>
  );
}
