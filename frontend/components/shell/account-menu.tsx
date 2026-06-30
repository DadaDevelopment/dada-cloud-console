"use client";
import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { useAuth } from "@/lib/auth";
import { useProjectContext } from "@/lib/project-context";
import { canApprove, roleColors } from "@/lib/rbac";
import { useT } from "@/lib/i18n/console/context";

/**
 * Top-right account menu. Consolidates identity, the project role badge, global
 * cross-project destinations (AI Studio, Approvals — the latter admin-only) and
 * sign-out, which previously lived as a bare link in the sidebar footer.
 */
export function AccountMenu() {
  const router = useRouter();
  const { t } = useT();
  const { user, logout } = useAuth();
  const { role, projects } = useProjectContext();
  // Role is project-scoped; outside a project fall back to "admin anywhere"
  // so org admins can still reach global approvals from the projects list.
  const showApprovals = canApprove(role) || projects.some((p) => canApprove(p.role));
  const [open, setOpen] = useState(false);
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

  const name = user?.display_name || user?.username || t("shell.account.fallbackName");
  const initial = name.charAt(0).toUpperCase();

  function handleLogout() {
    logout();
    router.push("/login");
  }

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t("shell.account.label")}
        className="flex h-9 w-9 items-center justify-center rounded-full bg-blue-600 text-sm font-semibold text-white transition-colors hover:bg-blue-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-400 focus-visible:ring-offset-2 focus-visible:ring-offset-slate-900"
      >
        {initial}
      </button>

      {open && (
        <div role="menu" className="absolute right-0 z-50 mt-2 w-64 overflow-hidden rounded-xl border border-gray-200 bg-white shadow-2xl dark:border-gray-800 dark:bg-gray-900">
          <div className="border-b border-gray-100 px-4 py-3 dark:border-gray-800">
            <p className="truncate text-sm font-semibold text-gray-900 dark:text-gray-100">{name}</p>
            {user?.email && <p className="truncate text-xs text-gray-400 dark:text-gray-500">{user.email}</p>}
            {role && (
              <span className={`mt-2 inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${roleColors[role]}`}>
                {t(`roles.${role}`)}
              </span>
            )}
          </div>
          <div className="py-1">
            <Link role="menuitem" href="/ai-studio" onClick={() => setOpen(false)} className="block px-4 py-2 text-sm text-gray-700 hover:bg-gray-50 dark:text-gray-200 dark:hover:bg-gray-800">
              AI Studio
            </Link>
            {showApprovals && (
              <Link role="menuitem" href="/admin/approvals" onClick={() => setOpen(false)} className="block px-4 py-2 text-sm text-gray-700 hover:bg-gray-50 dark:text-gray-200 dark:hover:bg-gray-800">
                {t("shell.account.approvals")}
              </Link>
            )}
          </div>
          <div className="border-t border-gray-100 py-1 dark:border-gray-800">
            <button
              role="menuitem"
              onClick={handleLogout}
              className="block w-full px-4 py-2 text-left text-sm text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/40"
            >
              {t("shell.account.signOut")}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
