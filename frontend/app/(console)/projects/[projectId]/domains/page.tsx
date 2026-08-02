"use client";
import { useCallback, useEffect, useMemo, useRef, useState, FormEvent } from "react";
import { useParams } from "next/navigation";
import { customDomainsApi, appsApi, managedDnsApi } from "@/lib/api";
import { QuotaUpsell } from "@/components/billing/quota-upsell";
import { docsHref } from "@/lib/site";
import type {
  DomainAuthorization,
  DomainChallenge,
  DomainHostname,
  ManagedZone,
  ResourceSnapshot,
} from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { ResourceZeroState } from "@/components/ui/resource-zero-state";
import { Globe } from "lucide-react";
import { StateChip } from "@/components/ui/state-chip";
import type { ChipTone } from "@/components/ui/state-chip";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { ManagedDnsPanel } from "@/components/deploy/managed-dns";
import { hostnameReason } from "@/lib/hostname-status";
import { useT } from "@/lib/i18n/console/context";

type TFn = (key: string, params?: Record<string, string>) => string;

/** Strip scheme/path/trailing-dot and lowercase a user-entered domain. */
function normalizeDomain(raw: string): string {
  let s = raw.trim().toLowerCase();
  const schemeIdx = s.indexOf("://");
  if (schemeIdx >= 0) s = s.slice(schemeIdx + 3);
  const slash = s.indexOf("/");
  if (slash >= 0) s = s.slice(0, slash);
  if (s.endsWith(".")) s = s.slice(0, -1);
  return s;
}

/**
 * Resolve the apex an entered hostname belongs to. Prefers an existing
 * authorization matched by suffix; otherwise falls back to the naive
 * last-two-labels registrable apex (good enough for the common .com/.ru case).
 */
function deriveApex(
  host: string,
  auths: DomainAuthorization[]
): { apex: string; existing: DomainAuthorization | null } {
  const match = auths.find((a) => host === a.apex_domain || host.endsWith("." + a.apex_domain));
  if (match) return { apex: match.apex_domain, existing: match };
  const parts = host.split(".");
  const apex = parts.length <= 2 ? host : parts.slice(-2).join(".");
  return { apex, existing: null };
}

/** The verified/known apex a hostname sorts under, or the hostname itself. */
function apexOf(host: string, auths: DomainAuthorization[]): string {
  const match = auths.find((a) => host === a.apex_domain || host.endsWith("." + a.apex_domain));
  return match?.apex_domain ?? host;
}

function CopyField({ label, value }: { label: string; value: string }) {
  const { t } = useT();
  const [copied, setCopied] = useState(false);
  return (
    <div>
      <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{label}</p>
      <div className="mt-1 flex items-center gap-2">
        <code className="flex-1 break-all rounded-md bg-gray-50 dark:bg-gray-900 border border-gray-200 dark:border-gray-800 px-3 py-2 text-xs text-gray-800 dark:text-gray-200">
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
    </div>
  );
}

function ChallengeBlock({ challenge }: { challenge: DomainChallenge }) {
  const { t } = useT();
  return (
    <div className="space-y-3 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4">
      <p className="text-sm text-gray-700 dark:text-gray-200">
        {t("domains.challenge.instruction", { type: challenge.type })}
      </p>
      <CopyField label={t("domains.challenge.fieldType")} value={challenge.type} />
      <CopyField label={t("domains.challenge.fieldHost")} value={challenge.host} />
      <CopyField label={t("domains.challenge.fieldValue")} value={challenge.value} />
    </div>
  );
}

function DnsHintBlock({ record }: { record: { type: string; host: string; target: string } }) {
  const { t } = useT();
  return (
    <div className="space-y-3 rounded-lg border border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/30 p-4">
      <p className="text-sm font-medium text-blue-800 dark:text-blue-200">{t("domains.hm.dnsTitle")}</p>
      <CopyField label={t("domains.challenge.fieldType")} value={record.type} />
      <CopyField label={t("domains.challenge.fieldHost")} value={record.host} />
      <CopyField label={t("domains.challenge.fieldValue")} value={record.target} />
      <p className="text-xs text-blue-700 dark:text-blue-300">{t("domains.hm.dnsNote")}</p>
    </div>
  );
}

function hostStatusChip(status: DomainHostname["status"], t: TFn) {
  const map: Record<DomainHostname["status"], { tone: ChipTone; label: string }> = {
    active: { tone: "ready", label: t("domains.hostStatus.active") },
    pending: { tone: "needs-action", label: t("domains.hostStatus.pending") },
    failed: { tone: "error", label: t("domains.hostStatus.failed") },
  };
  const s = map[status];
  return (
    <StateChip tone={s.tone} dot>
      {s.label}
    </StateChip>
  );
}

const btnGhost =
  "inline-flex items-center gap-1.5 rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-xs font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-50 transition-colors";
const btnDanger =
  "inline-flex items-center gap-1.5 rounded-lg border border-red-200 dark:border-red-900 px-3 py-1.5 text-xs font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/40 disabled:opacity-50 transition-colors";
const btnPrimary =
  "inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors";

export default function ProjectDomainsPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const { project, environments, role } = useProjectContext();
  const { t } = useT();
  const envId = environments[0]?.id ?? "";
  const canEdit = canMutate(role);

  const [auths, setAuths] = useState<DomainAuthorization[]>([]);
  const [hostnames, setHostnames] = useState<DomainHostname[]>([]);
  const [zones, setZones] = useState<Record<string, ManagedZone | null>>({});
  const [apps, setApps] = useState<ResourceSnapshot[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [funnelOpen, setFunnelOpen] = useState(false);
  const [funnelPrefill, setFunnelPrefill] = useState("");
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const authsRef = useRef<DomainAuthorization[]>([]);
  useEffect(() => {
    authsRef.current = auths;
  }, [auths]);

  const fetchAll = useCallback(async (): Promise<{
    auths: DomainAuthorization[];
    apps: ResourceSnapshot[];
    hostnames: DomainHostname[];
    zones: Record<string, ManagedZone | null>;
  }> => {
    const authResp = await customDomainsApi.listAuthorizations(projectId);
    const authList = authResp.authorizations ?? [];

    let appList: ResourceSnapshot[] = [];
    let hostList: DomainHostname[] = [];
    if (envId) {
      try {
        const appResp = await appsApi.list(projectId, envId);
        appList = appResp.apps ?? [];
        const perApp = await Promise.all(
          appList.map((a) =>
            customDomainsApi
              .listHostnames(projectId, envId, a.name)
              .then((r) => r.hostnames ?? [])
              .catch(() => [] as DomainHostname[])
          )
        );
        hostList = perApp.flat();
      } catch {
        appList = [];
        hostList = [];
      }
    }

    const verified = authList.filter((a) => a.status === "verified");
    const zoneEntries = await Promise.all(
      verified.map((a) =>
        managedDnsApi
          .getZone(projectId, a.id)
          .then((z) => [a.id, z] as const)
          .catch(() => [a.id, null] as const)
      )
    );

    return {
      auths: authList,
      apps: appList,
      hostnames: hostList,
      zones: Object.fromEntries(zoneEntries),
    };
  }, [projectId, envId]);

  const reload = useCallback(() => {
    return fetchAll()
      .then((d) => {
        setAuths(d.auths);
        setApps(d.apps);
        setHostnames(d.hostnames);
        setZones(d.zones);
        setError(null);
      })
      .catch((err) => setError(err instanceof Error ? err.message : t("domains.error.load")))
      .finally(() => setIsLoading(false));
  }, [fetchAll, t]);

  useEffect(() => {
    let alive = true;
    fetchAll()
      .then((d) => {
        if (!alive) return;
        setAuths(d.auths);
        setApps(d.apps);
        setHostnames(d.hostnames);
        setZones(d.zones);
        setError(null);
      })
      .catch((err) => {
        if (alive) setError(err instanceof Error ? err.message : t("domains.error.load"));
      })
      .finally(() => {
        if (alive) setIsLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [fetchAll, t]);

  useEffect(() => {
    const hasPending = auths.some((a) => a.status !== "verified");
    if (!hasPending || funnelOpen) return;
    const id = setInterval(() => {
      const targets = authsRef.current.filter((a) => a.status !== "verified");
      if (targets.length === 0) return;
      Promise.all(
        targets.map((a) =>
          customDomainsApi
            .verifyAuthorization(projectId, a.id)
            .then((r) => ({ ...r.authorization, challenge: r.challenge }))
            .catch(() => null)
        )
      ).then((results) => {
        const verifiedNow = results.some(
          (r, i) => r && r.status === "verified" && targets[i].status !== "verified"
        );
        setAuths((prev) => prev.map((a) => results.find((r) => r && r.id === a.id) ?? a));
        if (verifiedNow) void reload();
      });
    }, 15_000);
    return () => clearInterval(id);
  }, [auths, funnelOpen, projectId, reload]);

  async function handleVerify(id: string) {
    setBusyId(id);
    setError(null);
    try {
      const result = await customDomainsApi.verifyAuthorization(projectId, id);
      const updated = { ...result.authorization, challenge: result.challenge };
      setAuths((prev) => prev.map((a) => (a.id === id ? updated : a)));
      if (updated.status === "verified") void reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("domains.error.verify"));
    } finally {
      setBusyId(null);
    }
  }

  async function handleDeleteApex(id: string) {
    if (!confirm(t("domains.confirm.remove"))) return;
    setBusyId(id);
    setError(null);
    try {
      await customDomainsApi.deleteAuthorization(projectId, id);
      setAuths((prev) => prev.filter((a) => a.id !== id));
      setExpandedId((e) => (e === id ? null : e));
    } catch (err) {
      setError(err instanceof Error ? err.message : t("domains.error.delete"));
    } finally {
      setBusyId(null);
    }
  }

  async function handleDetach(h: DomainHostname) {
    if (!confirm(t("domains.hm.confirmDetach", { name: h.hostname }))) return;
    setBusyId(h.id);
    setError(null);
    try {
      await customDomainsApi.detachHostname(projectId, envId, h.app_name, h.id);
      setHostnames((prev) => prev.filter((x) => x.id !== h.id));
      setExpandedId((e) => (e === h.id ? null : e));
    } catch (err) {
      setError(err instanceof Error ? err.message : t("domains.hm.detachError"));
    } finally {
      setBusyId(null);
    }
  }

  async function refreshApp(appName: string, hostId: string) {
    setBusyId(hostId);
    try {
      const r = await customDomainsApi.listHostnames(projectId, envId, appName);
      setHostnames((prev) => [...prev.filter((h) => h.app_name !== appName), ...(r.hostnames ?? [])]);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("domains.hm.loadError"));
    } finally {
      setBusyId(null);
    }
  }

  function openFunnel(prefill = "") {
    setFunnelPrefill(prefill);
    setFunnelOpen(true);
  }

  const rows = useMemo(() => {
    const attachedApexes = new Set(hostnames.map((h) => h.hostname));
    type Row =
      | { kind: "host"; id: string; sortApex: string; sortSub: number; sortName: string; host: DomainHostname }
      | { kind: "apex-delegated"; id: string; sortApex: string; sortSub: number; sortName: string; auth: DomainAuthorization; zone: ManagedZone }
      | { kind: "apex-pending"; id: string; sortApex: string; sortSub: number; sortName: string; auth: DomainAuthorization }
      | { kind: "apex-verified"; id: string; sortApex: string; sortSub: number; sortName: string; auth: DomainAuthorization };

    const list: Row[] = [];

    for (const auth of auths) {
      if (auth.status !== "verified") {
        list.push({ kind: "apex-pending", id: auth.id, sortApex: auth.apex_domain, sortSub: 0, sortName: auth.apex_domain, auth });
        continue;
      }
      const zone = zones[auth.id];
      if (zone) {
        list.push({ kind: "apex-delegated", id: auth.id, sortApex: auth.apex_domain, sortSub: 0, sortName: auth.apex_domain, auth, zone });
        continue;
      }
      if (!attachedApexes.has(auth.apex_domain)) {
        list.push({ kind: "apex-verified", id: auth.id, sortApex: auth.apex_domain, sortSub: 0, sortName: auth.apex_domain, auth });
      }
    }

    for (const h of hostnames) {
      list.push({
        kind: "host",
        id: h.id,
        sortApex: apexOf(h.hostname, auths),
        sortSub: 1,
        sortName: h.hostname,
        host: h,
      });
    }

    list.sort((a, b) => {
      if (a.sortApex !== b.sortApex) return a.sortApex.localeCompare(b.sortApex);
      if (a.sortSub !== b.sortSub) return a.sortSub - b.sortSub;
      return a.sortName.localeCompare(b.sortName);
    });
    return list;
  }, [auths, hostnames, zones]);

  const nameCls = "font-mono text-sm font-semibold text-gray-900 dark:text-gray-100 break-all";
  const subCls = "mt-1 text-xs text-gray-500 dark:text-gray-400";

  return (
    <div>
      <div className="mb-8 flex flex-wrap items-start justify-between gap-3">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.projects"), href: "/projects" },
              { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
              { label: t("nav.domains") },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("domains.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("domains.subtitle")}</p>
        </div>
        {canEdit && (
          <button onClick={() => openFunnel()} className={btnPrimary}>
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            {t("domains.add")}
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
      ) : rows.length === 0 ? (
        <div>
          <ResourceZeroState
            tone="blue"
            icon={<Globe className="h-8 w-8" />}
            title={t("domains.empty.title")}
            description={t("domains.empty.description")}
            cta={canEdit ? { label: t("domains.empty.create"), onClick: () => openFunnel() } : undefined}
            steps={[t("domains.empty.step1"), t("domains.empty.step2"), t("domains.empty.step3")]}
          />
          <div className="mt-4 text-center">
            <a href={docsHref("domains-and-https")} target="_blank" rel="noopener noreferrer" className="text-sm font-medium text-blue-600 hover:text-blue-700">
              {t("common.learnMore")} →
            </a>
          </div>
        </div>
      ) : (
        <div className="space-y-3">
          {rows.map((row) => {
            const expanded = expandedId === row.id;
            const toggle = () => setExpandedId(expanded ? null : row.id);

            if (row.kind === "host") {
              const h = row.host;
              return (
                <div key={row.id} className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4 sm:p-5 shadow-sm">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className={nameCls}>{h.hostname}</span>
                        {hostStatusChip(h.status, t)}
                      </div>
                      <p className={subCls}>
                        {t("domains.row.pointsTo", { app: h.app_name })}
                        {h.managed ? ` · ${t("domains.hm.defaultBadge")}` : ""}
                      </p>
                      {hostnameReason(h.status_reason, t) && (
                        <p className="mt-1 text-xs text-amber-700 dark:text-amber-500">
                          {hostnameReason(h.status_reason, t)}
                        </p>
                      )}
                    </div>
                    {canEdit && (
                      <div className="flex shrink-0 items-center gap-2">
                        <button onClick={() => refreshApp(h.app_name, h.id)} disabled={busyId === h.id} className={btnGhost}>
                          {busyId === h.id ? <Spinner size="sm" /> : null}
                          {t("common.refresh")}
                        </button>
                        <button onClick={toggle} className={btnGhost}>
                          {t("domains.dns.edit")}
                        </button>
                      </div>
                    )}
                  </div>
                  {expanded && (
                    <div className="mt-4 space-y-4 border-t border-gray-100 dark:border-gray-800 pt-4">
                      <div className="flex flex-wrap items-center gap-4 text-xs text-gray-500 dark:text-gray-400">
                        <span>
                          {t("domains.hm.thRecord")}: <span className="font-mono text-gray-800 dark:text-gray-200">{h.record_type}</span>
                        </span>
                        <span className="flex items-center gap-1.5">
                          {t("domains.hm.thCert")}: <PhaseBadge phase={h.cert_status} />
                        </span>
                      </div>
                      <p className="text-xs text-gray-500 dark:text-gray-400">
                        {t("domains.edit.recordUnknown", { type: h.record_type })}
                      </p>
                      {canEdit && !h.managed && (
                        <button onClick={() => handleDetach(h)} disabled={busyId === h.id} className={btnDanger}>
                          {busyId === h.id ? t("domains.hm.detaching") : t("domains.hm.detach")}
                        </button>
                      )}
                    </div>
                  )}
                </div>
              );
            }

            if (row.kind === "apex-delegated") {
              const { auth, zone } = row;
              return (
                <div key={row.id} className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4 sm:p-5 shadow-sm">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className={nameCls}>{auth.apex_domain}</span>
                        <StateChip tone={zone.status === "active" ? "ready" : "needs-action"} dot>
                          {zone.status === "active" ? t("domains.dns.statusActive") : t("domains.dns.statusAwaiting")}
                        </StateChip>
                        <StateChip tone="backup">{t("domains.tag.delegated")}</StateChip>
                      </div>
                      <p className={`${subCls} font-mono break-all`}>{zone.nameservers.join("  ·  ")}</p>
                    </div>
                    <div className="flex shrink-0 items-center gap-2">
                      <button onClick={toggle} className={btnGhost}>
                        {t("domains.dns.edit")}
                      </button>
                    </div>
                  </div>
                  {expanded && (
                    <div className="mt-4 border-t border-gray-100 dark:border-gray-800 pt-4">
                      <ManagedDnsPanel projectId={projectId} authId={auth.id} apex={auth.apex_domain} canEdit={canEdit} />
                    </div>
                  )}
                </div>
              );
            }

            if (row.kind === "apex-pending") {
              const { auth } = row;
              const failed = auth.status === "failed";
              return (
                <div key={row.id} className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4 sm:p-5 shadow-sm">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className={nameCls}>{auth.apex_domain}</span>
                        <StateChip tone={failed ? "error" : "needs-action"} dot>
                          {failed ? t("domains.status.failed") : t("domains.apex.needsVerify")}
                        </StateChip>
                      </div>
                      <p className={subCls}>
                        {auth.error_message ? auth.error_message : t("domains.autoCheck")}
                      </p>
                    </div>
                    {canEdit && (
                      <div className="flex shrink-0 items-center gap-2">
                        <button onClick={() => handleVerify(auth.id)} disabled={busyId === auth.id} className={btnGhost}>
                          {busyId === auth.id ? <Spinner size="sm" /> : null}
                          {t("domains.action.verify")}
                        </button>
                        <button onClick={toggle} className={btnGhost}>
                          {t("domains.dns.edit")}
                        </button>
                      </div>
                    )}
                  </div>
                  {expanded && (
                    <div className="mt-4 space-y-4 border-t border-gray-100 dark:border-gray-800 pt-4">
                      {auth.challenge && <ChallengeBlock challenge={auth.challenge} />}
                      {canEdit && (
                        <button onClick={() => handleDeleteApex(auth.id)} disabled={busyId === auth.id} className={btnDanger}>
                          {busyId === auth.id ? t("common.removing") : t("common.remove")}
                        </button>
                      )}
                    </div>
                  )}
                </div>
              );
            }

            const { auth } = row;
            return (
              <div key={row.id} className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4 sm:p-5 shadow-sm">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className={nameCls}>{auth.apex_domain}</span>
                      <StateChip tone="ready" dot>
                        {t("domains.status.verified")}
                      </StateChip>
                    </div>
                    <p className={subCls}>{t("domains.apex.verifiedIdle")}</p>
                  </div>
                  {canEdit && (
                    <div className="flex shrink-0 items-center gap-2">
                      <button onClick={() => openFunnel(auth.apex_domain)} className={btnGhost}>
                        {t("domains.action.addHost")}
                      </button>
                      <button onClick={toggle} className={btnGhost}>
                        {t("domains.action.delegateEdit")}
                      </button>
                    </div>
                  )}
                </div>
                {expanded && (
                  <div className="mt-4 border-t border-gray-100 dark:border-gray-800 pt-4">
                    <ManagedDnsPanel projectId={projectId} authId={auth.id} apex={auth.apex_domain} canEdit={canEdit} />
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      <Modal isOpen={funnelOpen} onClose={() => setFunnelOpen(false)} title={t("domains.funnel.title")}>
        <AddDomainFunnel
          key={funnelPrefill + String(funnelOpen)}
          projectId={projectId}
          envId={envId}
          apps={apps}
          auths={auths}
          canEdit={canEdit}
          initialDomain={funnelPrefill}
          onChanged={reload}
          onClose={() => setFunnelOpen(false)}
        />
      </Modal>
    </div>
  );
}

type FunnelStep = "input" | "verify" | "path";

function AddDomainFunnel({
  projectId,
  envId,
  apps,
  auths,
  canEdit,
  initialDomain,
  onChanged,
  onClose,
}: {
  projectId: string;
  envId: string;
  apps: ResourceSnapshot[];
  auths: DomainAuthorization[];
  canEdit: boolean;
  initialDomain: string;
  onChanged: () => Promise<void> | void;
  onClose: () => void;
}) {
  const { t } = useT();
  const [step, setStep] = useState<FunnelStep>("input");
  const [domainInput, setDomainInput] = useState(initialDomain);
  const [targetHost, setTargetHost] = useState("");
  const [auth, setAuth] = useState<DomainAuthorization | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [quotaBlocked, setQuotaBlocked] = useState<{ resource: string; limit?: number } | null>(null);

  const [path, setPath] = useState<"app" | "delegate">("app");
  const [appName, setAppName] = useState(apps.length === 1 ? apps[0].name : "");
  const [dnsHint, setDnsHint] = useState<{ type: string; host: string; target: string } | null>(null);

  const authRef = useRef<DomainAuthorization | null>(null);
  useEffect(() => {
    authRef.current = auth;
  }, [auth]);

  useEffect(() => {
    if (step !== "verify" || !auth || auth.status === "verified") return;
    const id = setInterval(() => {
      const a = authRef.current;
      if (!a) return;
      customDomainsApi
        .verifyAuthorization(projectId, a.id)
        .then((r) => {
          const updated = { ...r.authorization, challenge: r.challenge };
          setAuth(updated);
          if (updated.status === "verified") {
            setStep("path");
            void onChanged();
          }
        })
        .catch(() => undefined);
    }, 10_000);
    return () => clearInterval(id);
  }, [step, auth, projectId, onChanged]);

  async function handleContinue(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setErr(null);
    setQuotaBlocked(null);
    const host = normalizeDomain(domainInput);
    if (!host) return;
    setTargetHost(host);
    const { apex, existing } = deriveApex(host, auths);
    setBusy(true);
    try {
      if (existing && existing.status === "verified") {
        setAuth(existing);
        setStep("path");
      } else if (existing) {
        setAuth(existing);
        setStep("verify");
      } else {
        const res = await customDomainsApi.addAuthorization(projectId, apex);
        const created = { ...res.authorization, challenge: res.challenge };
        setAuth(created);
        void onChanged();
        setStep(created.status === "verified" ? "path" : "verify");
      }
    } catch (e2) {
      const quota = e2 as { code?: string; resource?: string; limit?: number } | undefined;
      if (quota?.code === "quota_exceeded") {
        setQuotaBlocked({ resource: quota.resource ?? "domains", limit: quota.limit });
      } else {
        setErr(e2 instanceof Error ? e2.message : t("domains.error.add"));
      }
    } finally {
      setBusy(false);
    }
  }

  async function handleManualVerify() {
    if (!auth) return;
    setBusy(true);
    setErr(null);
    try {
      const r = await customDomainsApi.verifyAuthorization(projectId, auth.id);
      const updated = { ...r.authorization, challenge: r.challenge };
      setAuth(updated);
      if (updated.status === "verified") {
        setStep("path");
        void onChanged();
      }
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : t("domains.error.verify"));
    } finally {
      setBusy(false);
    }
  }

  async function handleAttach() {
    if (!appName) return;
    setBusy(true);
    setErr(null);
    try {
      const res = await customDomainsApi.attachHostname(projectId, envId, appName, targetHost);
      setDnsHint(res.dns_record);
      void onChanged();
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : t("domains.hm.attachError"));
    } finally {
      setBusy(false);
    }
  }

  function finish() {
    void onChanged();
    onClose();
  }

  if (step === "input") {
    return (
      <form onSubmit={handleContinue} className="space-y-4">
        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("domains.funnel.inputLabel")}</label>
          <input
            type="text"
            required
            autoFocus
            value={domainInput}
            onChange={(e) => setDomainInput(e.target.value)}
            placeholder="shop.acme.com"
            pattern="[A-Za-z0-9.\-]+"
            className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
          <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("domains.funnel.inputHelp")}</p>
        </div>
        {quotaBlocked && (
          <QuotaUpsell resource={quotaBlocked.resource} limit={quotaBlocked.limit} projectId={projectId} />
        )}
        {err && (
          <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
            {err}
          </div>
        )}
        <div className="flex justify-end gap-3 pt-2">
          <button type="button" onClick={onClose} className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors">
            {t("common.cancel")}
          </button>
          <button type="submit" disabled={busy} className={btnPrimary}>
            {busy ? <Spinner size="sm" /> : null}
            {t("domains.funnel.continue")}
          </button>
        </div>
      </form>
    );
  }

  if (step === "verify") {
    return (
      <div className="space-y-4">
        <div>
          <p className="text-sm font-medium text-gray-900 dark:text-gray-100">
            {t("domains.funnel.verifyTitle", { apex: auth?.apex_domain ?? "" })}
          </p>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("domains.funnel.verifyIntro")}</p>
        </div>
        {auth?.challenge && <ChallengeBlock challenge={auth.challenge} />}
        <p className="flex items-center gap-1.5 text-xs text-gray-400 dark:text-gray-500">
          <Spinner size="sm" /> {t("domains.funnel.verifyPending")}
        </p>
        {err && (
          <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
            {err}
          </div>
        )}
        <div className="flex justify-end gap-3 pt-2">
          <button type="button" onClick={onClose} className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors">
            {t("common.cancel")}
          </button>
          <button type="button" onClick={handleManualVerify} disabled={busy} className={btnPrimary}>
            {busy ? <Spinner size="sm" /> : null}
            {t("domains.action.verify")}
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <div>
        <p className="mb-2 text-sm font-medium text-gray-900 dark:text-gray-100">
          {t("domains.funnel.pathTitle", { domain: targetHost })}
        </p>
        <div className="flex gap-1 rounded-xl border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900 p-1">
          <button
            type="button"
            onClick={() => setPath("app")}
            aria-pressed={path === "app"}
            className={`flex-1 rounded-lg px-4 py-2 text-sm font-medium transition-colors ${
              path === "app" ? "bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100 shadow-sm" : "text-gray-500 dark:text-gray-400"
            }`}
          >
            {t("domains.funnel.pointApp")}
          </button>
          <button
            type="button"
            onClick={() => setPath("delegate")}
            aria-pressed={path === "delegate"}
            className={`flex-1 rounded-lg px-4 py-2 text-sm font-medium transition-colors ${
              path === "delegate" ? "bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100 shadow-sm" : "text-gray-500 dark:text-gray-400"
            }`}
          >
            {t("domains.path.delegate")}
          </button>
        </div>
      </div>

      {path === "app" ? (
        dnsHint ? (
          <div className="space-y-4">
            <DnsHintBlock record={dnsHint} />
            <div className="flex justify-end">
              <button type="button" onClick={finish} className={btnPrimary}>
                {t("domains.funnel.done")}
              </button>
            </div>
          </div>
        ) : (
          <div className="space-y-4">
            <div>
              <label className="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-200">{t("domains.hostnames.app")}</label>
              <select
                value={appName}
                onChange={(e) => setAppName(e.target.value)}
                disabled={apps.length === 0}
                className="w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2.5 text-sm text-gray-900 dark:text-gray-100 focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/30 disabled:opacity-60"
              >
                <option value="">{apps.length === 0 ? t("domains.hostnames.noApps") : t("domains.hostnames.selectApp")}</option>
                {apps.map((a) => (
                  <option key={a.id} value={a.name}>
                    {a.name}
                  </option>
                ))}
              </select>
            </div>
            {err && (
              <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
                {err}
              </div>
            )}
            <div className="flex justify-end gap-3">
              <button type="button" onClick={onClose} className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors">
                {t("common.cancel")}
              </button>
              <button type="button" onClick={handleAttach} disabled={busy || !appName} className={btnPrimary}>
                {busy ? <Spinner size="sm" /> : null}
                {t("domains.hm.attach")}
              </button>
            </div>
          </div>
        )
      ) : (
        <div className="space-y-4">
          {auth && <ManagedDnsPanel projectId={projectId} authId={auth.id} apex={auth.apex_domain} canEdit={canEdit} />}
          <div className="flex justify-end">
            <button type="button" onClick={finish} className={btnPrimary}>
              {t("domains.funnel.done")}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
