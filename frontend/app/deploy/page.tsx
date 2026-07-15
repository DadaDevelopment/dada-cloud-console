"use client";
/**
 * "Deploy to Dada" badge target. A GitHub template README points its badge at
 * `/deploy?repo=<owner>/<name>`, so a logged-out visitor lands here first.
 * Resolves auth + the caller's default project, then forwards into the git
 * import wizard with the repo pre-selected — removing the old badge -> bare
 * /register -> manual Git-import-search tax.
 *
 * Lives at the top level (not under the (console) route group) so it is
 * reachable unauthenticated on both the marketing and console hosts (proxy.ts
 * only special-cases "/" per host, every other path passes through as-is).
 */
import { Suspense, useEffect, useRef } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { startRegister } from "@/lib/register-redirect";
import { projectsApi } from "@/lib/api";
import { Spinner } from "@/components/ui/spinner";

function DeployResolver() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { isLoading, token } = useAuth();
  const startedRef = useRef(false);

  useEffect(() => {
    if (isLoading) return;
    if (startedRef.current) return;
    startedRef.current = true;

    const repo = searchParams.get("repo");

    if (!token) {
      const qs = searchParams.toString();
      void startRegister(`/deploy${qs ? `?${qs}` : ""}`);
      return;
    }

    if (!repo) {
      router.replace("/projects");
      return;
    }

    (async () => {
      try {
        const { projects } = await projectsApi.list();
        let projectId = projects[0]?.id ?? null;
        if (!projectId) {
          const created = await projectsApi.ensureDefault();
          projectId = created.project_id;
        }
        if (!projectId) {
          router.replace("/projects");
          return;
        }
        router.replace(`/projects/${projectId}/git/import?repo=${encodeURIComponent(repo)}`);
      } catch {
        router.replace("/projects");
      }
    })();
  }, [isLoading, token, router, searchParams]);

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50">
      <div className="flex flex-col items-center gap-4 text-center">
        <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-blue-600">
          <svg className="h-6 w-6 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M3 15a4 4 0 004 4h9a5 5 0 10-.1-9.999 5.002 5.002 0 10-9.78 2.096A4.001 4.001 0 003 15z"
            />
          </svg>
        </div>
        <div className="flex items-center gap-2 text-sm text-gray-500">
          <Spinner size="sm" />
          Готовим деплой…
        </div>
      </div>
    </div>
  );
}

export default function DeployPage() {
  return (
    <Suspense fallback={<div className="min-h-screen bg-gray-50" />}>
      <DeployResolver />
    </Suspense>
  );
}
