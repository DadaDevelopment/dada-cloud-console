"use client";
import { useEffect, useRef, useState } from "react";
import { useProjectContext } from "@/lib/project-context";
import type { Environment } from "@/lib/types";

function EnvDot({ env }: { env: Environment }) {
  return (
    <span className={`h-2 w-2 shrink-0 rounded-full ${env.type === "prod" ? "bg-green-500" : "bg-blue-400"}`} />
  );
}

/**
 * Persistent environment selector in the top bar. Replaces the per-page env
 * tabs whose selection was lost on navigation; the chosen env now lives in the
 * shared context (URL + localStorage) so it survives navigation and back/forward.
 */
export function EnvSelector() {
  const { environments, selectedEnv, setSelectedEnvId } = useProjectContext();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, [open]);

  if (environments.length === 0) return null;

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={`Environment: ${selectedEnv?.name ?? "none selected"}`}
        className="flex h-9 items-center gap-2 rounded-lg border border-slate-700 bg-slate-800 px-3 text-sm font-medium text-white transition-colors hover:bg-slate-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
      >
        {selectedEnv && <EnvDot env={selectedEnv} />}
        <span className="truncate">{selectedEnv?.name ?? "Environment"}</span>
        <svg className="h-4 w-4 shrink-0 text-slate-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5} aria-hidden="true">
          <path strokeLinecap="round" strokeLinejoin="round" d="M19.5 8.25l-7.5 7.5-7.5-7.5" />
        </svg>
      </button>

      {open && (
        <div role="listbox" className="absolute right-0 z-50 mt-2 w-56 overflow-hidden rounded-xl border border-gray-200 bg-white py-1 shadow-2xl">
          {environments.map((env) => {
            const active = env.id === selectedEnv?.id;
            return (
              <button
                key={env.id}
                role="option"
                aria-selected={active}
                onClick={() => {
                  setSelectedEnvId(env.id);
                  setOpen(false);
                }}
                className={`flex w-full items-center gap-2 px-3 py-2 text-left text-sm transition-colors ${
                  active ? "bg-blue-50 text-blue-700" : "text-gray-700 hover:bg-gray-50"
                }`}
              >
                <EnvDot env={env} />
                <span className="flex-1 truncate font-medium">{env.name}</span>
                <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                  env.runtime === "vm" ? "bg-amber-100 text-amber-700" : "bg-slate-100 text-slate-600"
                }`}>
                  {env.runtime === "vm" ? "VM" : "K8s"}
                </span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
