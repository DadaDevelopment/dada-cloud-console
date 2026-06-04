"use client";
import { useEffect, useMemo, useState, useCallback } from "react";
import { useRouter } from "next/navigation";
import { useProjectContext } from "@/lib/project-context";
import { visibleNavItems, projectHref, type IconName } from "@/lib/resources";
import { ResourceIcon } from "./icons";

interface Command {
  id: string;
  label: string;
  hint?: string;
  icon?: IconName;
  run: () => void;
}

/**
 * ⌘K / Ctrl+K command palette. A fast keyboard path to any project or — when
 * inside a project — any resource section, mirroring the GCP `/` search and AWS
 * search bar. Client-side over already-loaded lists; a server-backed search can
 * slot in later behind the same UI.
 */
export function CommandPalette({ initialOpen = false }: { initialOpen?: boolean }) {
  const router = useRouter();
  const { projects, projectId, role } = useProjectContext();
  const [open, setOpen] = useState(initialOpen);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);

  const close = useCallback(() => {
    setOpen(false);
    setQuery("");
    setActive(0);
  }, []);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((o) => !o);
      }
      if (e.key === "Escape") close();
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [close]);

  const commands: Command[] = useMemo(() => {
    const cmds: Command[] = [];
    if (projectId) {
      for (const item of visibleNavItems(role)) {
        if (item.comingSoon) continue;
        cmds.push({
          id: `nav-${item.key}`,
          label: item.label,
          hint: "Go to",
          icon: item.icon,
          run: () => router.push(projectHref(projectId, item)),
        });
      }
    }
    for (const p of projects) {
      cmds.push({
        id: `proj-${p.id}`,
        label: p.display_name,
        hint: "Project",
        run: () => router.push(`/projects/${p.id}`),
      });
    }
    return cmds;
  }, [projectId, role, projects, router]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return commands.slice(0, 8);
    return commands.filter((c) => c.label.toLowerCase().includes(q)).slice(0, 8);
  }, [commands, query]);

  if (!open) return null;

  function runAt(idx: number) {
    const cmd = filtered[idx];
    if (cmd) {
      cmd.run();
      close();
    }
  }

  return (
    <div className="fixed inset-0 z-[100] flex items-start justify-center pt-[15vh]" role="dialog" aria-modal="true" aria-label="Command palette">
      <div className="absolute inset-0 bg-black/40 backdrop-blur-sm" onClick={close} aria-hidden="true" />
      <div className="relative z-10 mx-4 w-full max-w-lg overflow-hidden rounded-xl bg-white shadow-2xl">
        <input
          autoFocus
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown") {
              e.preventDefault();
              setActive((a) => Math.min(a + 1, filtered.length - 1));
            } else if (e.key === "ArrowUp") {
              e.preventDefault();
              setActive((a) => Math.max(a - 1, 0));
            } else if (e.key === "Enter") {
              e.preventDefault();
              runAt(active);
            }
          }}
          placeholder="Search projects and resources…"
          className="w-full border-b border-gray-100 px-4 py-3.5 text-sm text-gray-900 placeholder:text-gray-400 focus:outline-none"
          aria-label="Search"
        />
        <ul className="max-h-80 overflow-y-auto py-1">
          {filtered.length === 0 ? (
            <li className="px-4 py-6 text-center text-sm text-gray-400">No matches</li>
          ) : (
            filtered.map((c, idx) => (
              <li key={c.id}>
                <button
                  onMouseEnter={() => setActive(idx)}
                  onClick={() => runAt(idx)}
                  className={`flex w-full items-center justify-between gap-2 px-4 py-2.5 text-left text-sm transition-colors ${
                    idx === active ? "bg-blue-50 text-blue-700" : "text-gray-700"
                  }`}
                >
                  <span className="flex items-center gap-2.5">
                    {c.icon && <ResourceIcon name={c.icon} className="h-4 w-4 text-gray-400" />}
                    {c.label}
                  </span>
                  {c.hint && <span className="text-xs text-gray-400">{c.hint}</span>}
                </button>
              </li>
            ))
          )}
        </ul>
        <div className="flex items-center gap-3 border-t border-gray-100 px-4 py-2 text-xs text-gray-400">
          <span><kbd className="rounded border border-gray-200 px-1">↑↓</kbd> navigate</span>
          <span><kbd className="rounded border border-gray-200 px-1">↵</kbd> open</span>
          <span><kbd className="rounded border border-gray-200 px-1">esc</kbd> close</span>
        </div>
      </div>
    </div>
  );
}
