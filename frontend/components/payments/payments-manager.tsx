"use client";
import { useCallback, useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import { paymentsApi } from "@/lib/api";
import type { PaymentsConnection } from "@/lib/api";
import { paymentsEnvKeys, paymentsWebhooks } from "@/lib/payments-connection";
import { useT } from "@/lib/i18n/console/context";

interface Props {
  projectId: string;
  envId: string;
  appName: string;
  canEdit: boolean;
}

/**
 * PaymentsManager connects an app's YooKassa shop via OAuth. Connect issues an
 * authorize_url from the backend and navigates there in the same tick as the
 * triggering click (no await after the URL is known) so Safari/WebKit in-app
 * browsers don't drop the navigation's transient user-activation; a visible
 * fallback link covers the case where the redirect is dropped anyway.
 */
export function PaymentsManager({ projectId, envId, appName, canEdit }: Props) {
  const { t } = useT();
  const searchParams = useSearchParams();
  const [connection, setConnection] = useState<PaymentsConnection | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [authorizeUrl, setAuthorizeUrl] = useState<string | null>(null);
  const [notConfigured, setNotConfigured] = useState(false);
  const [connectError, setConnectError] = useState<string | null>(null);
  const [disconnecting, setDisconnecting] = useState(false);
  const [disconnectError, setDisconnectError] = useState<string | null>(null);
  const [snippetLang, setSnippetLang] = useState<"python" | "node">("python");
  const [queryMsg, setQueryMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(() => {
    if (searchParams.get("connected") === "1") {
      return { kind: "ok", text: t("apps.payments.connected.success") };
    }
    if (searchParams.get("payments_error")) {
      return { kind: "err", text: t("apps.payments.error.query") };
    }
    return null;
  });

  const load = useCallback(() => {
    if (!envId) return;
    paymentsApi
      .get(projectId, envId, appName)
      .then((d) => setConnection(d))
      .catch((e) => {
        const status = (e as { status?: number } | undefined)?.status;
        if (status === 404) setConnection(null);
      })
      .finally(() => setLoaded(true));
  }, [projectId, envId, appName]);

  useEffect(() => {
    load();
  }, [load]);

  async function handleConnect() {
    setConnectError(null);
    setNotConfigured(false);
    setConnecting(true);
    try {
      const resp = await paymentsApi.connect(projectId, envId, appName);
      setAuthorizeUrl(resp.authorize_url);
      window.location.href = resp.authorize_url;
    } catch (e) {
      const status = (e as { status?: number } | undefined)?.status;
      if (status === 409) {
        setNotConfigured(true);
      } else {
        setConnectError(e instanceof Error ? e.message : t("apps.payments.error.connect"));
      }
    } finally {
      setConnecting(false);
    }
  }

  async function handleDisconnect() {
    if (!window.confirm(t("apps.payments.disconnect.confirm"))) return;
    setDisconnectError(null);
    setDisconnecting(true);
    try {
      await paymentsApi.disconnect(projectId, envId, appName);
      setConnection(null);
      setQueryMsg({ kind: "ok", text: t("apps.payments.disconnect.success") });
    } catch (e) {
      setDisconnectError(e instanceof Error ? e.message : t("apps.payments.disconnect.error"));
    } finally {
      setDisconnecting(false);
    }
  }

  const pythonSnippet = `import os
import uuid
import requests

response = requests.post(
    "https://api.yookassa.ru/v3/payments",
    auth=None,
    headers={
        "Authorization": f"Bearer {os.environ['YOOKASSA_OAUTH_TOKEN']}",
        "Idempotence-Key": str(uuid.uuid4()),
        "Content-Type": "application/json",
    },
    json={
        "amount": {"value": "100.00", "currency": "RUB"},
        "confirmation": {"type": "redirect", "return_url": "https://example.com/thanks"},
        "capture": True,
        "description": "Order #1",
    },
)
print(response.json())`;

  const nodeSnippet = `const { randomUUID } = require("crypto");

const response = await fetch("https://api.yookassa.ru/v3/payments", {
  method: "POST",
  headers: {
    "Authorization": \`Bearer \${process.env.YOOKASSA_OAUTH_TOKEN}\`,
    "Idempotence-Key": randomUUID(),
    "Content-Type": "application/json",
  },
  body: JSON.stringify({
    amount: { value: "100.00", currency: "RUB" },
    confirmation: { type: "redirect", return_url: "https://example.com/thanks" },
    capture: true,
    description: "Order #1",
  }),
});
console.log(await response.json());`;

  const statusBadgeClass =
    connection?.status === "active"
      ? "bg-green-50 text-green-700 dark:bg-green-950/40 dark:text-green-400"
      : connection?.status === "error"
        ? "bg-red-50 text-red-700 dark:bg-red-950/40 dark:text-red-400"
        : "bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300";

  const statusLabel =
    connection?.status === "active"
      ? t("apps.payments.status.active")
      : connection?.status === "error"
        ? t("apps.payments.status.error")
        : t("apps.payments.status.disconnected");

  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-6">
      <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("apps.payments.title")}</h2>
      <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("apps.payments.subtitle")}</p>

      {queryMsg && (
        <p
          className={`mt-3 text-sm ${
            queryMsg.kind === "ok" ? "text-green-600 dark:text-green-400" : "text-red-600 dark:text-red-400"
          }`}
        >
          {queryMsg.text}
        </p>
      )}

      {!loaded && <p className="mt-4 text-sm text-gray-400 dark:text-gray-500">...</p>}

      {loaded && !connection && (
        <div className="mt-4">
          <button
            onClick={handleConnect}
            disabled={!canEdit || connecting}
            className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {connecting ? t("apps.payments.connecting") : t("apps.payments.connect")}
          </button>

          {authorizeUrl && (
            <a
              href={authorizeUrl}
              className="mt-3 block text-xs font-medium text-blue-600 dark:text-blue-400 hover:underline"
            >
              {t("apps.payments.openAuthorize")}
            </a>
          )}

          {notConfigured && (
            <p className="mt-3 text-sm text-amber-600 dark:text-amber-500">{t("apps.payments.notConfigured")}</p>
          )}

          {connectError && <p className="mt-3 text-sm text-red-600 dark:text-red-400">{connectError}</p>}
        </div>
      )}

      {loaded && connection && (
        <div className="mt-5 space-y-5">
          <div className="flex flex-wrap items-center gap-3">
            <span className="text-sm text-gray-500 dark:text-gray-400">{t("apps.payments.status.label")}:</span>
            <span className={`rounded-full px-2.5 py-0.5 text-xs font-medium ${statusBadgeClass}`}>{statusLabel}</span>
          </div>

          <div className="grid gap-3 sm:grid-cols-2 text-sm">
            <div>
              <span className="text-gray-500 dark:text-gray-400">{t("apps.payments.accountId")}: </span>
              <span className="font-mono text-gray-900 dark:text-gray-100">{connection.account_id ?? "-"}</span>
            </div>
            {connection.expires_at && (
              <div>
                <span className="text-gray-500 dark:text-gray-400">{t("apps.payments.expiresAt")}: </span>
                <span className="text-gray-900 dark:text-gray-100">{connection.expires_at}</span>
              </div>
            )}
            {connection.connected_at && (
              <div>
                <span className="text-gray-500 dark:text-gray-400">{t("apps.payments.connectedAt")}: </span>
                <span className="text-gray-900 dark:text-gray-100">{connection.connected_at}</span>
              </div>
            )}
          </div>

          <div>
            <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("apps.payments.webhooks.title")}</h3>
            {connection.webhook_note === "no_public_hostname" ? (
              <p className="mt-1 text-sm text-amber-600 dark:text-amber-500">{t("apps.payments.webhooks.noHostnameWarning")}</p>
            ) : paymentsWebhooks(connection).length === 0 ? (
              <p className="mt-1 text-sm text-gray-400 dark:text-gray-500">{t("apps.payments.webhooks.none")}</p>
            ) : (
              <ul className="mt-1 space-y-1 text-sm">
                {paymentsWebhooks(connection).map((w) => (
                  <li key={w.id} className="font-mono text-gray-700 dark:text-gray-300">
                    {w.event} <span className="text-gray-400 dark:text-gray-500">({w.id})</span>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div>
            <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("apps.payments.envKeys.title")}</h3>
            <ul className="mt-1 space-y-1 text-sm font-mono text-gray-700 dark:text-gray-300">
              {paymentsEnvKeys(connection).map((k) => (
                <li key={k}>{k}</li>
              ))}
            </ul>
            <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("apps.payments.envKeys.hint")}</p>
          </div>

          <div>
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("apps.payments.snippet.title")}</h3>
              <div className="ml-auto flex gap-1">
                <button
                  onClick={() => setSnippetLang("python")}
                  className={`rounded px-2 py-1 text-xs font-medium ${
                    snippetLang === "python"
                      ? "bg-blue-600 text-white"
                      : "bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300"
                  }`}
                >
                  {t("apps.payments.snippet.python")}
                </button>
                <button
                  onClick={() => setSnippetLang("node")}
                  className={`rounded px-2 py-1 text-xs font-medium ${
                    snippetLang === "node"
                      ? "bg-blue-600 text-white"
                      : "bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300"
                  }`}
                >
                  {t("apps.payments.snippet.node")}
                </button>
              </div>
            </div>
            <pre className="mt-2 overflow-x-auto rounded-lg bg-gray-50 dark:bg-gray-950 px-4 py-3 text-xs text-gray-800 dark:text-gray-200">
              {snippetLang === "python" ? pythonSnippet : nodeSnippet}
            </pre>
          </div>

          {disconnectError && <p className="text-sm text-red-600 dark:text-red-400">{disconnectError}</p>}

          <button
            onClick={handleDisconnect}
            disabled={!canEdit || disconnecting}
            className="rounded-lg border border-red-300 dark:border-red-800 px-4 py-2 text-sm font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/30 disabled:opacity-50"
          >
            {disconnecting ? t("apps.payments.disconnecting") : t("apps.payments.disconnect")}
          </button>
        </div>
      )}
    </div>
  );
}
