"use client";
import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useProjectContext } from "@/lib/project-context";
import { useT } from "@/lib/i18n/console/context";
import { CreateProjectModal } from "./create-project-modal";

function ChevronUpDown({ className }: { className?: string }) {
  return (
    <svg className={className} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5} aria-hidden="true">
      <path strokeLinecap="round" strokeLinejoin="round" d="M8.25 9l3.75-3.75L15.75 9M8.25 15l3.75 3.75L15.75 15" />
    </svg>
  );
}

/**
 * Top-bar project switcher. Lets the user jump to any accessible project in a
 * single click from anywhere — replacing the previous "go back to the flat
 * /projects list" round trip. Mirrors the GCP/Yandex/VK folder picker pattern.
 */
export function ProjectSwitcher() {
  const router = useRouter();
  const { t } = useT();
  const { projects, project, projectId, refetchProjects } = useProjectContext();
  const [open, setOpen] = useState(false);
  const [showCreate, setShowCreate] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const label = project?.display_name ?? (projectId ? "…" : t("shell.project.select"));

  return (
    <div ref={ref} className="relative min-w-0">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="listbox"
        aria-expanded={open}
        className="flex h-9 w-full max-w-[14rem] items-center gap-2 rounded-lg border border-slate-700 bg-slate-800 px-3 text-sm font-medium text-white transition-colors hover:bg-slate-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
      >
        <span className="truncate">{label}</span>
        <ChevronUpDown className="h-4 w-4 shrink-0 text-slate-400" />
      </button>

      {open && (
        <div
          role="listbox"
          className="absolute left-0 z-50 mt-2 w-72 overflow-hidden rounded-xl border border-gray-200 bg-white shadow-2xl"
        >
          <div className="max-h-80 overflow-y-auto py-1">
            {projects.length === 0 ? (
              <div className="px-3 py-2 text-sm text-gray-500">{t("shell.project.none")}</div>
            ) : (
              projects.map((p) => {
                const active = p.id === projectId;
                return (
                  <button
                    key={p.id}
                    role="option"
                    aria-selected={active}
                    onClick={() => {
                      setOpen(false);
                      if (!active) router.push(`/projects/${p.id}`);
                    }}
                    className={`flex w-full items-center justify-between gap-2 px-3 py-2 text-left text-sm transition-colors ${
                      active ? "bg-blue-50 text-blue-700" : "text-gray-700 hover:bg-gray-50"
                    }`}
                  >
                    <span className="min-w-0">
                      <span className="block truncate font-medium">{p.display_name}</span>
                      <span className="block truncate font-mono text-xs text-gray-500">{p.name}</span>
                    </span>
                    {active && (
                      <svg className="h-4 w-4 shrink-0 text-blue-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                        <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
                      </svg>
                    )}
                  </button>
                );
              })
            )}
          </div>
          <button
            type="button"
            onClick={() => {
              setOpen(false);
              setShowCreate(true);
            }}
            className="flex w-full items-center gap-2 border-t border-gray-100 px-3 py-2.5 text-left text-sm font-medium text-blue-600 hover:bg-gray-50"
          >
            <svg className="h-4 w-4 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            {t("shell.project.create")}
          </button>
        </div>
      )}

      {showCreate && (
        <CreateProjectModal
          onClose={() => setShowCreate(false)}
          onCreated={(newId) => {
            setShowCreate(false);
            refetchProjects();
            router.push(`/projects/${newId}`);
          }}
        />
      )}
    </div>
  );
}
