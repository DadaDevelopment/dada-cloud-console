"use client";
import { useEffect, useRef, useState } from "react";
import { useParams, useSearchParams } from "next/navigation";
import { s3bucketsApi } from "@/lib/api";
import type { ResourceSnapshot, S3BucketCredentialsResponse } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { CopyButton } from "@/components/ui/copy-button";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";
import { HardDrive } from "lucide-react";

interface BucketSummary {
  bucket_name?: string;
  region?: string;
  public?: boolean;
  ftp_sftp_enable?: boolean;
  app_ref?: string;
}

type CredsErrorKind = "notReady" | "notConfigured" | "failed" | "generic";

/**
 * Object-storage bucket detail. Bucket metadata comes from `summary_json`; S3
 * access credentials are never included there and are fetched on demand from
 * the credentials endpoint (sensitive + audited server-side), then rendered
 * into an aws-cli usage example.
 */
export default function BucketDetailPage() {
  const params = useParams<{ projectId: string; name: string }>();
  const { projectId, name } = params;
  const searchParams = useSearchParams();
  const { project, selectedEnv, role } = useProjectContext();
  const { t } = useT();
  const envId = searchParams.get("envId") || selectedEnv?.id || "";
  const canManage = canMutate(role);

  const [bucket, setBucket] = useState<ResourceSnapshot | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [revealSecret, setRevealSecret] = useState(false);

  const [creds, setCreds] = useState<S3BucketCredentialsResponse | null>(null);
  const [credsLoading, setCredsLoading] = useState(false);
  const [credsError, setCredsError] = useState<{ kind: CredsErrorKind; message?: string } | null>(null);

  const [waitingSince, setWaitingSince] = useState<number | null>(null);
  const [waitedMin, setWaitedMin] = useState(0);

  async function revealCreds(silent = false) {
    if (!silent) setCredsLoading(true);
    setCredsError(null);
    try {
      const r = await s3bucketsApi.credentials(projectId, envId, name);
      setCreds(r);
    } catch (e) {
      const err = e as { status?: number; code?: string; provisioningSince?: string } | undefined;
      if (err?.status === 409 || err?.code === "provisioning_failed") {
        setCredsError({ kind: "failed", message: e instanceof Error ? e.message : t("storage.detail.access.error") });
      } else if (err?.status === 404) {
        setCredsError({ kind: "notReady" });
        const serverSince = err.provisioningSince ? Date.parse(err.provisioningSince) : NaN;
        if (!Number.isNaN(serverSince)) {
          setWaitingSince((prev) => (prev === null ? serverSince : Math.min(prev, serverSince)));
        } else {
          setWaitingSince((prev) => prev ?? Date.now());
        }
      } else if (err?.status === 503) {
        setCredsError({ kind: "notConfigured" });
      } else {
        setCredsError({ kind: "generic", message: e instanceof Error ? e.message : t("storage.detail.access.error") });
      }
    } finally {
      if (!silent) setCredsLoading(false);
    }
  }

  const revealRef = useRef(revealCreds);
  useEffect(() => {
    revealRef.current = revealCreds;
  });

  /**
   * Beget provisions an S3 bucket through Terraform and it can take over an
   * hour; the credentials endpoint answers 404 until the connection secret
   * lands. Users were re-clicking "Reveal" by hand and giving up minutes before
   * the bucket went live, so once we know the bucket is merely not ready we
   * keep asking on their behalf until it is (or until it fails for real).
   */
  useEffect(() => {
    if (credsError?.kind !== "notReady" || creds) return;
    const id = setInterval(() => void revealRef.current(true), 15000);
    return () => clearInterval(id);
  }, [credsError?.kind, creds]);

  useEffect(() => {
    if (waitingSince === null || creds) return;
    const tick = () => setWaitedMin(Math.floor((Date.now() - waitingSince) / 60000));
    tick();
    const id = setInterval(tick, 30000);
    return () => clearInterval(id);
  }, [waitingSince, creds]);

  useEffect(() => {
    if (!envId) return;
    s3bucketsApi
      .list(projectId, envId)
      .then((data) => {
        const found = (data.buckets ?? []).find((b) => b.name === name);
        if (!found) setError(t("storage.detail.notFound"));
        else setBucket(found);
      })
      .catch((err) => setError(err instanceof Error ? err.message : t("storage.error.load")))
      .finally(() => setIsLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, name, envId]);

  if (isLoading) {
    return <div className="flex h-64 items-center justify-center"><Spinner size="lg" /></div>;
  }
  if (error || !bucket) {
    return (
      <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
        {error ?? t("storage.detail.notFound")}
      </div>
    );
  }

  const s = (bucket.summary_json ?? {}) as BucketSummary;
  const bucketName = s.bucket_name ?? bucket.name;
  const region = s.region ?? "ru1";
  const endpoint = creds?.endpoint ?? "";
  const accessKey = creds?.access_key ?? "";
  const secretKey = creds?.secret_key ?? "";

  const endpointPlaceholder = endpoint || "<your-s3-endpoint>";
  const cli = `aws configure set aws_access_key_id ${accessKey || "<ACCESS_KEY>"}
aws configure set aws_secret_access_key ${secretKey || "<SECRET_KEY>"}

aws --endpoint-url ${endpointPlaceholder} s3 ls s3://${bucketName}/
aws --endpoint-url ${endpointPlaceholder} s3 cp ./file.txt s3://${bucketName}/
aws --endpoint-url ${endpointPlaceholder} s3 cp s3://${bucketName}/file.txt ./`;

  const rows: { label: string; value: string }[] = [
    { label: t("storage.detail.field.bucket"), value: bucketName },
    { label: t("storage.detail.field.region"), value: region },
    {
      label: t("storage.detail.field.visibility"),
      value: s.public ? t("storage.detail.visibility.public") : t("storage.detail.visibility.private"),
    },
    { label: t("storage.detail.field.ftp"), value: s.ftp_sftp_enable ? t("storage.detail.on") : t("storage.detail.off") },
    { label: t("storage.detail.field.appRef"), value: s.app_ref || t("storage.detail.envLevel") },
  ];

  return (
    <div>
      <div className="mb-8">
        <Breadcrumb
          items={[
            { label: t("common.crumb.projects"), href: "/projects" },
            { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
            { label: t("nav.storage"), href: `/projects/${projectId}/storage` },
            { label: bucket.name },
          ]}
        />
        <div className="mt-2 flex items-center gap-3">
          <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-amber-50 dark:bg-amber-950/40 text-amber-600 dark:text-amber-400">
            <HardDrive className="h-5 w-5" />
          </span>
          <h1 className="font-mono text-2xl font-bold text-gray-900 dark:text-gray-100">{bucket.name}</h1>
          <PhaseBadge phase={bucket.phase} />
        </div>
        <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">
          {t("common.status.synced", { ago: timeAgo(bucket.last_synced_at) })}
        </p>
      </div>

      <div className="space-y-6">
        <section className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
          <h2 className="mb-4 text-sm font-semibold text-gray-900 dark:text-gray-100">{t("storage.detail.overview")}</h2>
          <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {rows.map((r) => (
              <div key={r.label}>
                <dt className="text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">{r.label}</dt>
                <dd className="mt-1 truncate font-mono text-sm text-gray-900 dark:text-gray-100">{r.value}</dd>
              </div>
            ))}
          </dl>
        </section>

        <section className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
          <h2 className="mb-4 text-sm font-semibold text-gray-900 dark:text-gray-100">{t("storage.detail.access.title")}</h2>
          {creds ? (
            <div className="space-y-3">
              <SecretField label={t("storage.detail.access.endpoint")} value={endpoint} copyLabel={t("common.copy")} />
              <SecretField label={t("storage.detail.access.accessKey")} value={accessKey} copyLabel={t("common.copy")} />
              <div>
                <div className="flex items-center justify-between">
                  <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("storage.detail.access.secretKey")}</p>
                  <button
                    type="button"
                    onClick={() => setRevealSecret((v) => !v)}
                    className="text-xs font-medium text-blue-600 hover:text-blue-700"
                  >
                    {revealSecret ? t("storage.detail.access.hide") : t("storage.detail.access.reveal")}
                  </button>
                </div>
                <div className="mt-1 flex items-center gap-2">
                  <code className="flex-1 break-all rounded-md border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900 px-3 py-2 font-mono text-xs text-gray-800 dark:text-gray-200">
                    {revealSecret ? secretKey : "•".repeat(Math.min(secretKey.length, 40))}
                  </code>
                  <CopyButton value={secretKey} label={t("common.copy")} />
                </div>
              </div>
            </div>
          ) : canManage ? (
            <div>
              <button
                type="button"
                onClick={() => void revealCreds()}
                disabled={credsLoading}
                className="inline-flex items-center gap-2 rounded-lg bg-amber-600 px-4 py-2 text-sm font-medium text-white hover:bg-amber-700 disabled:opacity-50 transition-colors"
              >
                {credsLoading ? <><Spinner size="sm" /> {t("storage.detail.access.revealing")}</> : t("storage.detail.access.revealBtn")}
              </button>
              {credsError && credsError.kind === "failed" && (
                <div
                  data-ux="s3_provision_error"
                  className="mt-3 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-3 py-2"
                >
                  <p className="text-sm font-semibold text-red-700 dark:text-red-300">
                    {t("storage.detail.access.failedTitle")}
                  </p>
                  <p className="mt-1 break-words font-mono text-xs text-red-700 dark:text-red-300">
                    {credsError.message}
                  </p>
                  <p className="mt-1.5 text-xs text-red-600/90 dark:text-red-400/90">
                    {t("storage.detail.access.failedHint")}
                  </p>
                </div>
              )}
              {credsError && credsError.kind !== "failed" && (
                <p className={`mt-3 text-sm ${credsError.kind === "generic" ? "text-red-600 dark:text-red-400" : "text-gray-500 dark:text-gray-400"}`}>
                  {credsError.kind === "notReady"
                    ? t("storage.detail.access.waiting", { min: String(waitedMin) })
                    : credsError.kind === "notConfigured"
                      ? t("storage.detail.access.notConfigured")
                      : credsError.message}
                </p>
              )}
              {credsError?.kind === "notReady" && waitedMin > 90 && (
                <div
                  data-ux="s3_provision_slow"
                  className="mt-3 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-3 py-2"
                >
                  <p className="text-sm font-semibold text-amber-700 dark:text-amber-300">
                    {t("storage.detail.access.slowTitle")}
                  </p>
                  <p className="mt-1 text-xs text-amber-700/90 dark:text-amber-400/90">
                    {t("storage.detail.access.slowHint")}
                  </p>
                </div>
              )}
            </div>
          ) : (
            <p className="text-sm text-gray-500 dark:text-gray-400">{t("storage.detail.access.none")}</p>
          )}
        </section>

        <section className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
          <div className="mb-2 flex items-center justify-between">
            <h2 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("storage.detail.cli.title")}</h2>
            <CopyButton value={cli} label={t("common.copy")} />
          </div>
          <p className="mb-3 text-xs text-gray-500 dark:text-gray-400">{t("storage.detail.cli.hint")}</p>
          <pre className="overflow-x-auto rounded-lg bg-gray-900 px-4 py-4 font-mono text-xs leading-relaxed text-gray-200">
            {cli}
          </pre>
        </section>
      </div>
    </div>
  );
}

function SecretField({ label, value, copyLabel }: { label: string; value: string; copyLabel: string }) {
  return (
    <div>
      <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{label}</p>
      <div className="mt-1 flex items-center gap-2">
        <code className="flex-1 break-all rounded-md border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900 px-3 py-2 font-mono text-xs text-gray-800 dark:text-gray-200">
          {value}
        </code>
        <CopyButton value={value} label={copyLabel} />
      </div>
    </div>
  );
}
