"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { monitoringApi } from "@/lib/api";
import type { MonitoringMetricsResponse } from "@/lib/types";

export interface MetricsQueryParams {
  projectId: string;
  envId: string;
  appId: string;
  range?: string;
  from?: number;
  to?: number;
  groupBy?: string;
  agg?: string;
  filters?: string[];
  /** Poll interval in ms; 0 disables polling. */
  refreshMs?: number;
}

export interface MetricsQueryResult {
  data: MonitoringMetricsResponse | null;
  error: string | null;
  loading: boolean;
  refresh: () => void;
}

/**
 * useMetricsQuery fetches the resource's metric panels and re-fetches on param
 * change and on a poll timer. It dedupes overlapping responses via a request
 * sequence guard (so a slow earlier fetch can't clobber a newer one) and keeps
 * the last good data visible while a refresh is in flight (no loading flash).
 */
export function useMetricsQuery(params: MetricsQueryParams): MetricsQueryResult {
  const {
    projectId,
    envId,
    appId,
    range,
    from,
    to,
    groupBy,
    agg,
    filters,
    refreshMs = 0,
  } = params;

  const [data, setData] = useState<MonitoringMetricsResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const seq = useRef(0);

  const filterKey = (filters ?? []).slice().sort().join(",");

  const load = useCallback(async () => {
    const mine = ++seq.current;
    try {
      const d = await monitoringApi.getMetrics(projectId, envId, appId, {
        range,
        from,
        to,
        groupBy: groupBy || undefined,
        agg: agg || undefined,
        filters,
      });
      if (mine !== seq.current) return;
      setData(d);
      setError(null);
    } catch (e) {
      if (mine !== seq.current) return;
      setError(e instanceof Error ? e.message : "Failed to load metrics");
    } finally {
      if (mine === seq.current) setLoading(false);
    }
    // filterKey captures filters; envId/appId/projectId via deps.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, envId, appId, range, from, to, groupBy, agg, filterKey]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true);
    void load();
    if (!refreshMs) return;
    const id = setInterval(() => void load(), refreshMs);
    return () => clearInterval(id);
  }, [load, refreshMs]);

  return { data, error, loading, refresh: load };
}
