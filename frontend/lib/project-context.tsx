"use client";
// Shared project + environment context for the console shell.
//
// Why this exists: previously every page re-fetched the project, the sidebar
// had no idea which project was active, and each list page kept its own
// `selectedEnv` useState that reset on navigation. This provider makes project
// context part of the chrome:
//   - `projects`           — the switcher's list (fetched once)
//   - `project/role/envs`  — the active project, derived from the URL
//   - `selectedEnv`        — persisted to URL (?env=) + localStorage so it
//                            survives navigation, deep links and the back button
//
// Roles are project-scoped, so `role` here is the role *in the active project*
// and is what every rbac.ts check should be fed.

import {
  createContext,
  useContext,
  useEffect,
  useState,
  useCallback,
  useMemo,
  useRef,
} from "react";
import { useRouter, usePathname, useSearchParams } from "next/navigation";
import { projectsApi, isSignupClosedError } from "./api";
import type { Project, Environment, MemberRole } from "./types";

interface ProjectContextValue {
  // switcher
  projects: Project[];
  projectsLoading: boolean;
  // the project the console treats as home (first/only project, or the
  // auto-provisioned default). null until the list has loaded.
  defaultProjectId: string | null;
  // active project (null when not inside /projects/[id]/*)
  projectId: string | null;
  project: Project | null;
  environments: Environment[];
  role: MemberRole | undefined;
  loading: boolean;
  error: string | null;
  refetch: () => void;
  // re-fetch the switcher list (e.g. after creating a project).
  refetchProjects: () => void;
  /**
   * Set when the projects-list call - the first authenticated request the
   * console makes after login - came back 403 `signup_closed`: the caller's
   * identity is new and self-serve registration is currently closed. The
   * shell must show a dead-end screen instead of a project switcher with
   * zero projects, which otherwise reads as a blank console.
   */
  signupClosed: boolean;
  // environment selection
  selectedEnv: Environment | null;
  setSelectedEnvId: (envId: string) => void;
}

const ProjectContext = createContext<ProjectContextValue | null>(null);

function projectIdFromPath(pathname: string): string | null {
  const segs = pathname.split("/").filter(Boolean);
  if (segs[0] === "projects" && segs[1]) return segs[1];
  return null;
}

function envStorageKey(projectId: string): string {
  return `dada_env_${projectId}`;
}

export function ProjectProvider({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const projectId = projectIdFromPath(pathname);

  const [projects, setProjects] = useState<Project[]>([]);
  const [projectsLoading, setProjectsLoading] = useState(true);
  const [signupClosed, setSignupClosed] = useState(false);

  const [project, setProject] = useState<Project | null>(null);
  const [environments, setEnvironments] = useState<Environment[]>([]);
  const [role, setRole] = useState<MemberRole | undefined>(undefined);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selectedEnvId, setSelectedEnvIdState] = useState<string>("");
  const [reloadKey, setReloadKey] = useState(0);
  const [projectsReloadKey, setProjectsReloadKey] = useState(0);

  // The project the console treats as home: the first/only project, or null while
  // the list is still loading.
  const defaultProjectId = projects.length > 0 ? projects[0].id : null;

  // The project the current env selection was initialised for, so switching
  // projects re-resolves the env instead of carrying the old one over.
  const envInitFor = useRef<string | null>(null);
  // Guards the one-shot default-project bootstrap so an empty list provisions a
  // default exactly once per session, not on every refetch.
  const bootstrapped = useRef(false);

  // Projects list (switcher). Refetchable so creating a project updates the list.
  useEffect(() => {
    let cancelled = false;
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setProjectsLoading(true);
    setSignupClosed(false);
    projectsApi
      .list()
      .then(async (d) => {
        if (cancelled) return;
        const list = d.projects ?? [];
        // Empty list → auto-provision the default project once, then adopt it so
        // the user lands inside a project instead of an empty overview.
        if (list.length === 0 && !bootstrapped.current) {
          bootstrapped.current = true;
          try {
            const def = await projectsApi.ensureDefault();
            if (cancelled) return;
            const refreshed = await projectsApi.list();
            if (cancelled) return;
            setProjects(refreshed.projects ?? []);
            if (projectIdFromPath(window.location.pathname) === null) {
              router.replace(`/projects/${def.project_id}`);
            }
            return;
          } catch {
            if (!cancelled) setProjects([]);
            return;
          }
        }
        setProjects(list);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setProjects([]);
        const e = err as { status?: number; code?: string } | undefined;
        setSignupClosed(isSignupClosedError(e?.status, e?.code));
      })
      .finally(() => !cancelled && setProjectsLoading(false));
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectsReloadKey]);

  // Active project detail.
  useEffect(() => {
    if (!projectId) {
      // Clear active-project state when navigating out of a project scope.
      /* eslint-disable react-hooks/set-state-in-effect */
      setProject(null);
      setEnvironments([]);
      setRole(undefined);
      setError(null);
      /* eslint-enable react-hooks/set-state-in-effect */
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    projectsApi
      .get(projectId)
      .then((d) => {
        if (cancelled) return;
        setProject(d.project);
        setEnvironments(d.environments ?? []);
        setRole(d.role ?? d.project.role);
      })
      .catch((err) => {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : "Failed to load project");
      })
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [projectId, reloadKey]);

  // Resolve the selected environment once per project:
  //   URL ?env= → localStorage → project.default_environment → first env.
  useEffect(() => {
    if (!projectId || environments.length === 0) return;
    const stillValid = environments.some((e) => e.id === selectedEnvId);
    if (envInitFor.current === projectId && stillValid) return;

    const byId = (id: string | null | undefined) =>
      id ? environments.find((e) => e.id === id) : undefined;
    const byName = (name: string | null | undefined) =>
      name ? environments.find((e) => e.name === name) : undefined;

    const stored =
      typeof window !== "undefined" ? localStorage.getItem(envStorageKey(projectId)) : null;

    const resolved =
      byId(searchParams.get("env")) ??
      byId(stored) ??
      byName(project?.default_environment) ??
      environments[0];

    envInitFor.current = projectId;
    if (resolved) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setSelectedEnvIdState(resolved.id);
      if (typeof window !== "undefined") {
        localStorage.setItem(envStorageKey(projectId), resolved.id);
      }
    }
  }, [projectId, environments, project, searchParams, selectedEnvId]);

  const setSelectedEnvId = useCallback(
    (envId: string) => {
      setSelectedEnvIdState(envId);
      if (projectId && typeof window !== "undefined") {
        localStorage.setItem(envStorageKey(projectId), envId);
      }
      const params = new URLSearchParams(searchParams.toString());
      params.set("env", envId);
      router.replace(`${pathname}?${params.toString()}`, { scroll: false });
    },
    [projectId, pathname, router, searchParams],
  );

  const refetch = useCallback(() => setReloadKey((k) => k + 1), []);
  const refetchProjects = useCallback(() => setProjectsReloadKey((k) => k + 1), []);

  const selectedEnv = useMemo(
    () => environments.find((e) => e.id === selectedEnvId) ?? null,
    [environments, selectedEnvId],
  );

  const value: ProjectContextValue = {
    projects,
    projectsLoading,
    defaultProjectId,
    projectId,
    project,
    environments,
    role,
    loading,
    error,
    refetch,
    refetchProjects,
    signupClosed,
    selectedEnv,
    setSelectedEnvId,
  };

  return <ProjectContext.Provider value={value}>{children}</ProjectContext.Provider>;
}

export function useProjectContext(): ProjectContextValue {
  const ctx = useContext(ProjectContext);
  if (!ctx) throw new Error("useProjectContext must be used within ProjectProvider");
  return ctx;
}
