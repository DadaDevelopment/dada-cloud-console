"use client";
/**
 * "Deploy on Dada" badge target. A README badge points at
 * `/deploy?repo=<owner>/<name>` (a full GitHub URL works too), so a visitor —
 * usually someone who does NOT own the repository — lands here first.
 *
 * The page resolves auth + the caller's default project, detects the framework
 * anonymously, and then links + builds the public repo directly with no GitHub
 * App installation. It deliberately does NOT forward into the git-import
 * wizard: that wizard can only select repos reachable through the visitor's own
 * installations, so a foreign public repo never resolved there and the badge
 * dead-ended at the GitHub-connect wall.
 *
 * Lives at the top level (not under the (console) route group) so it is
 * reachable unauthenticated on both the marketing and console hosts (proxy.ts
 * only special-cases "/" per host, every other path passes through as-is).
 */
import { Suspense, useCallback, useEffect, useRef, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { startRegister } from "@/lib/register-redirect";
import { projectsApi, gitApi, buildsApi } from "@/lib/api";
import type { FrameworkDetection } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import { githubUrl, parseRepoParam } from "@/lib/deploy-badge";

function toKubeName(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "")
    .slice(0, 63);
}

function suffix(): string {
  return Math.random().toString(36).slice(2, 6);
}

type Target = { projectId: string; envId: string };

function DeployResolver() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { isLoading, token } = useAuth();
  const startedRef = useRef(false);

  const repo = parseRepoParam(searchParams.get("repo"));
  const branch = (searchParams.get("branch") || "main").trim() || "main";
  const rootDir = (searchParams.get("rootDir") || searchParams.get("root_dir") || ".").trim() || ".";

  const defaultAppName = repo ? toKubeName(repo.split("/")[1]) : "";

  const [target, setTarget] = useState<Target | null>(null);
  const [detection, setDetection] = useState<FrameworkDetection | null>(null);
  const [appNameInput, setAppNameInput] = useState("");
  const appName = appNameInput || defaultAppName;
  const [port, setPort] = useState(8080);
  const portTouchedRef = useRef(false);
  const [resolving, setResolving] = useState(true);
  const [deploying, setDeploying] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (isLoading) return;
    if (startedRef.current) return;
    startedRef.current = true;

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
        let envId: string | null = null;
        if (!projectId) {
          const created = await projectsApi.ensureDefault();
          projectId = created.project_id;
          envId = created.default_environment_id;
        }
        if (!projectId) {
          router.replace("/projects");
          return;
        }
        if (!envId) {
          const detail = await projectsApi.get(projectId);
          envId = detail.environments[0]?.id ?? null;
        }
        if (!envId) {
          router.replace(`/projects/${projectId}`);
          return;
        }
        setTarget({ projectId, envId });
        setResolving(false);

        try {
          const det = await gitApi.detectPublic(projectId, repo, rootDir);
          setDetection(det);
          const detected = det.port;
          if (typeof detected === "number" && detected > 0 && !portTouchedRef.current) {
            setPort(detected);
          }
        } catch {
          setDetection(null);
        }
      } catch (err) {
        setResolving(false);
        setError(err instanceof Error ? err.message : "Не удалось подготовить деплой");
      }
    })();
  }, [isLoading, token, router, searchParams, repo, rootDir]);

  const deploy = useCallback(async () => {
    if (!target || !repo || deploying) return;
    const wanted = toKubeName(appName) || toKubeName(repo.split("/")[1]);
    setError(null);
    setDeploying(true);

    /**
     * Links under the requested name, falling back to a suffixed name when it
     * is taken. Never reuses the existing app: a name collision in a shared
     * project usually means an unrelated app, and building it would deploy
     * someone else's code under this badge click.
     */
    const link = async (name: string): Promise<string> => {
      try {
        await gitApi.linkRepo(target.projectId, target.envId, {
          installation_id: "",
          repo_full_name: repo,
          app_name: name,
          production_branch: branch,
          root_dir: rootDir,
          auto_deploy: false,
          port,
          profile: "small",
        });
        return name;
      } catch (err) {
        const msg = err instanceof Error ? err.message : "Не удалось привязать репозиторий";
        if (!/409|already|conflict|уже/i.test(msg) || name !== wanted) throw new Error(msg);
        return link(toKubeName(`${wanted}-${suffix()}`));
      }
    };

    try {
      const name = await link(wanted);
      await buildsApi.trigger(target.projectId, target.envId, name);
      router.replace(`/projects/${target.projectId}/apps/${name}/deployments?envId=${target.envId}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Деплой не запустился");
      setDeploying(false);
    }
  }, [target, repo, deploying, appName, branch, rootDir, port, router]);

  if (!token || resolving || !repo) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-gray-50 dark:bg-gray-950">
        <div className="flex flex-col items-center gap-4 text-center">
          <DeployMark />
          <div className="flex items-center gap-2 text-sm text-gray-500">
            <Spinner size="sm" />
            Готовим деплой…
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50 px-4 py-10 dark:bg-gray-950">
      <div className="w-full max-w-md rounded-2xl border border-gray-200 bg-white p-8 shadow-sm dark:border-gray-800 dark:bg-gray-900">
        <div className="flex flex-col items-center gap-3 text-center">
          <DeployMark />
          <h1 className="text-xl font-semibold text-gray-900 dark:text-gray-50">
            Развернуть в Dada Cloud
          </h1>
          <a
            href={githubUrl(repo)}
            target="_blank"
            rel="noreferrer noopener"
            className="font-mono text-sm text-blue-600 hover:underline dark:text-blue-400"
          >
            {repo}
          </a>
          {detection?.framework ? (
            <p className="text-sm text-gray-500 dark:text-gray-400">
              Определили: {detection.framework}
            </p>
          ) : null}
        </div>

        <div className="mt-6 space-y-4">
          <label className="block">
            <span className="text-sm font-medium text-gray-700 dark:text-gray-200">
              Имя приложения
            </span>
            <input
              value={appName}
              onChange={(e) => setAppNameInput(e.target.value)}
              className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100"
              spellCheck={false}
            />
          </label>
          <label className="block">
            <span className="text-sm font-medium text-gray-700 dark:text-gray-200">Порт</span>
            <input
              type="number"
              value={port}
              min={1}
              max={65535}
              onChange={(e) => {
                portTouchedRef.current = true;
                setPort(Number(e.target.value));
              }}
              className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100"
            />
          </label>
          <p className="text-xs text-gray-500 dark:text-gray-400">
            Ветка <span className="font-mono">{branch}</span>
            {rootDir !== "." ? (
              <>
                , каталог <span className="font-mono">{rootDir}</span>
              </>
            ) : null}
          </p>
        </div>

        {error ? (
          <p className="mt-4 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">
            {error}
          </p>
        ) : null}

        <Button
          className="mt-6 w-full"
          size="lg"
          onClick={() => void deploy()}
          isLoading={deploying}
          disabled={!appName.trim() || port < 1 || port > 65535}
        >
          {deploying ? "Деплоим…" : "Развернуть"}
        </Button>
        <p className="mt-3 text-center text-xs text-gray-500 dark:text-gray-400">
          Публичный репозиторий клонируется анонимно — подключать GitHub не нужно.
        </p>
      </div>
    </div>
  );
}

function DeployMark() {
  return (
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
  );
}

export default function DeployPage() {
  return (
    <Suspense fallback={<div className="min-h-screen bg-gray-50 dark:bg-gray-950" />}>
      <DeployResolver />
    </Suspense>
  );
}
