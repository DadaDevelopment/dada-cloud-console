/**
 * The default-project bootstrap: what {@link ProjectProvider} runs when the
 * projects list comes back empty, so a brand-new user lands inside a project
 * instead of a blank switcher. Pulled out of the provider's effect as a pure
 * async step (deps injected, no React) so the failure branch - previously a
 * silent `catch { setProjects([]) }` that left the user on an empty screen
 * with zero signal - can be unit tested without rendering anything.
 *
 * Never branches on error prose: `signup_closed` is told apart from any
 * other failure strictly by status+code, via {@link isSignupClosedError}.
 */
import { isSignupClosedError } from "./api.ts";
import type { Project, CreateProjectResponse, ProjectsResponse } from "./types.ts";

export interface BootstrapFailure {
  status?: number;
  code?: string;
}

export interface BootstrapDeps {
  ensureDefault: () => Promise<CreateProjectResponse>;
  listProjects: () => Promise<ProjectsResponse>;
}

export type BootstrapOutcome =
  | { status: "provisioned"; projectId: string; projects: Project[] }
  | { status: "signupClosed" }
  | { status: "error"; failure: BootstrapFailure };

export async function runDefaultProjectBootstrap(deps: BootstrapDeps): Promise<BootstrapOutcome> {
  try {
    const def = await deps.ensureDefault();
    const refreshed = await deps.listProjects();
    return { status: "provisioned", projectId: def.project_id, projects: refreshed.projects ?? [] };
  } catch (err) {
    const e = err as { status?: number; code?: string } | undefined;
    if (isSignupClosedError(e?.status, e?.code)) {
      return { status: "signupClosed" };
    }
    return { status: "error", failure: { status: e?.status, code: e?.code } };
  }
}
