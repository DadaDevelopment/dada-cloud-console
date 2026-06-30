"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { monitoringApi } from "@/lib/api";
import {
  DASHBOARD_VERSION,
  defaultDashboardState,
  type DashboardState,
} from "./dashboard-types";

const KEY_PREFIX = "dada.dashboard.v1";
const SAVE_DEBOUNCE_MS = 800;

function storageKey(projectId: string, appId: string): string {
  return `${KEY_PREFIX}:${projectId}:${appId}`;
}

/** Reads the localStorage cache, honoring the version gate. */
function readCache(projectId: string, appId: string): DashboardState | null {
  try {
    const raw = localStorage.getItem(storageKey(projectId, appId));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as DashboardState;
    return parsed.version === DASHBOARD_VERSION ? parsed : null;
  } catch {
    return null;
  }
}

/** Coerces an arbitrary server blob into a valid state, gating on version. */
function fromServer(config: unknown): DashboardState | null {
  if (!config || typeof config !== "object") return null;
  const s = config as DashboardState;
  return s.version === DASHBOARD_VERSION && Array.isArray(s.panels) ? s : null;
}

/**
 * useDashboardState persists the whole dashboard config (range, refresh, filters,
 * group-by, aggregation, panel layout) PER USER on the backend, with localStorage
 * as an offline cache for an instant first paint. On mount it paints the cache
 * immediately, then reconciles with the server. Edits update in place
 * (optimistic), write the cache synchronously, and debounce a PUT to the API.
 * A version mismatch (cache or server) falls back to defaults.
 */
export function useDashboardState(
  projectId: string,
  envId: string,
  appId: string,
): [DashboardState, (patch: Partial<DashboardState> | ((s: DashboardState) => DashboardState)) => void, () => void] {
  const [state, setState] = useState<DashboardState>(defaultDashboardState);

  // Gate persistence until the initial server reconcile completes, so an empty
  // default never clobbers a stored server dashboard before it loads.
  const hydrated = useRef(false);
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const lastSaved = useRef<string>("");

  useEffect(() => {
    hydrated.current = false;
    let alive = true;

    const cached = readCache(projectId, appId);
    if (cached) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setState(cached);
      lastSaved.current = JSON.stringify(cached);
    } else {
      setState(defaultDashboardState());
    }

    monitoringApi
      .getDashboard(projectId, envId, appId)
      .then((res) => {
        if (!alive) return;
        const server = fromServer(res.config);
        if (server) {
          setState(server);
          lastSaved.current = JSON.stringify(server);
          try {
            localStorage.setItem(storageKey(projectId, appId), JSON.stringify(server));
          } catch {
            /* cache write best-effort */
          }
        }
      })
      .catch(() => {
        /* offline / not found → keep cache or default */
      })
      .finally(() => {
        if (alive) hydrated.current = true;
      });

    return () => {
      alive = false;
    };
  }, [projectId, envId, appId]);

  // Cache write + debounced server save on every change, once hydrated.
  useEffect(() => {
    try {
      localStorage.setItem(storageKey(projectId, appId), JSON.stringify(state));
    } catch {
      /* storage full/blocked → state still lives in memory */
    }
    if (!hydrated.current) return;
    const serialized = JSON.stringify(state);
    if (serialized === lastSaved.current) return;

    if (saveTimer.current) clearTimeout(saveTimer.current);
    saveTimer.current = setTimeout(() => {
      lastSaved.current = serialized;
      monitoringApi.saveDashboard(projectId, envId, appId, state, state.version).catch(() => {
        // Save failed → allow a retry on the next change.
        lastSaved.current = "";
      });
    }, SAVE_DEBOUNCE_MS);

    return () => {
      if (saveTimer.current) clearTimeout(saveTimer.current);
    };
  }, [projectId, envId, appId, state]);

  const update = useCallback(
    (patch: Partial<DashboardState> | ((s: DashboardState) => DashboardState)) => {
      setState((prev) => (typeof patch === "function" ? patch(prev) : { ...prev, ...patch }));
    },
    [],
  );

  const reset = useCallback(() => setState(defaultDashboardState()), []);

  return [state, update, reset];
}
