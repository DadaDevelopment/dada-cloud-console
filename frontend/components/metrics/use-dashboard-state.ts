"use client";
import { useCallback, useEffect, useState } from "react";
import {
  DASHBOARD_VERSION,
  defaultDashboardState,
  type DashboardState,
} from "./dashboard-types";

const KEY_PREFIX = "dada.dashboard.v1";

function storageKey(projectId: string, appId: string): string {
  return `${KEY_PREFIX}:${projectId}:${appId}`;
}

/**
 * useDashboardState persists the whole dashboard config (range, refresh, filters,
 * group-by, aggregation, panel layout) to localStorage per project+resource, so
 * a reload restores exactly what the user arranged. A version mismatch falls back
 * to defaults rather than crashing on a stale shape.
 */
export function useDashboardState(
  projectId: string,
  appId: string,
): [DashboardState, (patch: Partial<DashboardState> | ((s: DashboardState) => DashboardState)) => void, () => void] {
  const [state, setState] = useState<DashboardState>(defaultDashboardState);

  useEffect(() => {
    try {
      const raw = localStorage.getItem(storageKey(projectId, appId));
      if (raw) {
        const parsed = JSON.parse(raw) as DashboardState;
        if (parsed.version === DASHBOARD_VERSION) {
          // eslint-disable-next-line react-hooks/set-state-in-effect
          setState(parsed);
          return;
        }
      }
    } catch {
      /* ignore corrupt/blocked storage → defaults */
    }
    setState(defaultDashboardState());
  }, [projectId, appId]);

  useEffect(() => {
    try {
      localStorage.setItem(storageKey(projectId, appId), JSON.stringify(state));
    } catch {
      /* storage full/blocked → state still lives in memory */
    }
  }, [projectId, appId, state]);

  const update = useCallback(
    (patch: Partial<DashboardState> | ((s: DashboardState) => DashboardState)) => {
      setState((prev) => (typeof patch === "function" ? patch(prev) : { ...prev, ...patch }));
    },
    [],
  );

  const reset = useCallback(() => setState(defaultDashboardState()), []);

  return [state, update, reset];
}
