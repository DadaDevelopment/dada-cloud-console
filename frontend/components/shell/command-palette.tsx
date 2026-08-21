"use client";
import { useEffect, useMemo, useState, useCallback, useRef } from "react";
import { useRouter } from "next/navigation";
import { useProjectContext } from "@/lib/project-context";
import { searchApi } from "@/lib/api";
import type { SearchAppHit, SearchProjectHit } from "@/lib/types";
import { visibleNavItems, projectHref, type IconName } from "@/lib/resources";
import { useT } from "@/lib/i18n/console/context";
import { ResourceIcon } from "./icons";

/**
 * The MCP setup guide lives on the marketing host, and until now the console
 * never mentioned MCP at all -- a user told us they went looking for the server
 * and gave up. ⌘K is the cheapest place to make it findable from inside the
 * product.
 */
const MCP_DOCS_URL = "https://cloud.dada-tuda.ru/developer/mcp-ai-agents";

interface Command {
  id: string;
  label: string;
  hint?: string;
  icon?: IconName;
  run: () => void;
}

/** Debounce before the palette asks the server, in milliseconds. */
const SEARCH_DEBOUNCE_MS = 200;

/** Minimum query length the search endpoint answers. */
const SEARCH_MIN_CHARS = 2;

/**
 * The last answered search, tagged with the query it answers. Keeping the query
 * alongside the hits lets the palette tell "results for what you typed" from
 * "results for what you typed three keystrokes ago" without clearing state from
 * inside the effect.
 */
interface SearchResult {
  query: string;
  apps: SearchAppHit[];
  projects: SearchProjectHit[];
}

const EMPTY_APPS: SearchAppHit[] = [];
const EMPTY_PROJECTS: SearchProjectHit[] = [];

/**
 * ⌘K / Ctrl+K command palette. A fast keyboard path to any project, any app in
 * any project, or — when inside a project — any resource section, mirroring the
 * GCP `/` search and AWS search bar.
 *
 * Projects and nav sections are matched client-side over already-loaded lists.
 * Apps cannot be: the console only ever loads the app list of the project and
 * environment currently open, so "where does app X live?" was unanswerable and
 * an admin looking at dozens of projects had to open them one by one. Typing two
 * characters therefore also fires a debounced /search call, whose hits are
 * merged in below the local ones.
 */
export function CommandPalette({ initialOpen = false }: { initialOpen?: boolean }) {
  const router = useRouter();
  const { t } = useT();
  const { projects, projectId, role } = useProjectContext();
  const [open, setOpen] = useState(initialOpen);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [result, setResult] = useState<SearchResult | null>(null);
  const seqRef = useRef(0);

  const close = useCallback(() => {
    setOpen(false);
    setQuery("");
    setActive(0);
    setResult(null);
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

  useEffect(() => {
    const q = query.trim();
    if (!open || q.length < SEARCH_MIN_CHARS) return;
    const seq = ++seqRef.current;
    const timer = setTimeout(() => {
      searchApi
        .query(q)
        .then((res) => {
          if (seqRef.current !== seq) return;
          setResult({ query: q, apps: res.apps, projects: res.projects });
        })
        .catch(() => {
          if (seqRef.current !== seq) return;
          setResult({ query: q, apps: [], projects: [] });
        });
    }, SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [query, open]);

  const trimmed = query.trim();
  const fresh = result && result.query === trimmed ? result : null;
  const remoteApps = fresh?.apps ?? EMPTY_APPS;
  const remoteProjects = fresh?.projects ?? EMPTY_PROJECTS;
  const searching = open && trimmed.length >= SEARCH_MIN_CHARS && !fresh;

  const commands: Command[] = useMemo(() => {
    const cmds: Command[] = [];
    if (projectId) {
      for (const item of visibleNavItems(role)) {
        if (item.comingSoon) continue;
        cmds.push({
          id: `nav-${item.key}`,
          label: t(`nav.${item.key}`),
          hint: t("shell.palette.goTo"),
          icon: item.icon,
          run: () => router.push(projectHref(projectId, item)),
        });
      }
    }
    for (const p of projects) {
      cmds.push({
        id: `proj-${p.id}`,
        label: p.display_name,
        hint: t("shell.palette.project"),
        run: () => router.push(`/projects/${p.id}`),
      });
    }
    cmds.push({
      id: "docs-mcp",
      label: t("shell.palette.mcp"),
      hint: t("shell.palette.docs"),
      run: () => window.open(MCP_DOCS_URL, "_blank", "noopener,noreferrer"),
    });
    return cmds;
  }, [projectId, role, projects, router, t]);

  const remoteCommands: Command[] = useMemo(() => {
    const cmds: Command[] = [];
    for (const a of remoteApps) {
      const where = a.project_display_name || a.project_name;
      const env = a.environment_name ? ` / ${a.environment_name}` : "";
      const href = `/projects/${a.project_id}/apps/${encodeURIComponent(a.name)}${
        a.environment_id ? `?env=${a.environment_id}` : ""
      }`;
      cmds.push({
        id: `app-${a.project_id}-${a.environment_id}-${a.name}`,
        label: a.name,
        hint: `${t("shell.palette.app")} · ${where}${env}`,
        icon: "apps",
        run: () => router.push(href),
      });
    }
    const known = new Set(projects.map((p) => p.id));
    for (const p of remoteProjects) {
      if (known.has(p.id)) continue;
      cmds.push({
        id: `remote-proj-${p.id}`,
        label: p.display_name || p.name,
        hint: t("shell.palette.project"),
        run: () => router.push(`/projects/${p.id}`),
      });
    }
    return cmds;
  }, [remoteApps, remoteProjects, projects, router, t]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return commands.slice(0, 8);
    const local = commands.filter((c) => c.label.toLowerCase().includes(q));
    const localIds = new Set(local.map((c) => c.id));
    const remote = remoteCommands.filter((c) => !localIds.has(c.id));
    return [...local, ...remote].slice(0, 12);
  }, [commands, remoteCommands, query]);

  if (!open) return null;

  function runAt(idx: number) {
    const cmd = filtered[idx];
    if (cmd) {
      cmd.run();
      close();
    }
  }

  return (
    <div className="fixed inset-0 z-[100] flex items-start justify-center pt-[15vh]" role="dialog" aria-modal="true" aria-label={t("shell.palette.label")}>
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
          placeholder={t("shell.palette.placeholder")}
          className="w-full border-b border-gray-100 px-4 py-3.5 text-sm text-gray-900 placeholder:text-gray-400 focus:outline-none"
          aria-label={t("shell.search")}
        />
        <ul className="max-h-80 overflow-y-auto py-1">
          {filtered.length === 0 ? (
            <li className="px-4 py-6 text-center text-sm text-gray-500">
              {searching ? t("shell.palette.searching") : t("shell.palette.noMatches")}
            </li>
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
                  {c.hint && <span className="text-xs text-gray-500">{c.hint}</span>}
                </button>
              </li>
            ))
          )}
        </ul>
        <div className="flex items-center gap-3 border-t border-gray-100 px-4 py-2 text-xs text-gray-500">
          <span><kbd className="rounded border border-gray-200 px-1">↑↓</kbd> {t("shell.palette.navigate")}</span>
          <span><kbd className="rounded border border-gray-200 px-1">↵</kbd> {t("shell.palette.open")}</span>
          <span><kbd className="rounded border border-gray-200 px-1">esc</kbd> {t("shell.palette.close")}</span>
        </div>
      </div>
    </div>
  );
}
