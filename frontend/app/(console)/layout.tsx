"use client";
import { Suspense, useEffect, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { Spinner } from "@/components/ui/spinner";
import { ProjectProvider, useProjectContext } from "@/lib/project-context";
import { ConsoleLangProvider } from "@/lib/i18n/console/context";
import { ThemeProvider } from "@/lib/theme/context";
import { TopBar } from "@/components/shell/top-bar";
import { ProjectNav } from "@/components/shell/project-nav";
import { CommandPalette } from "@/components/shell/command-palette";
import { ConsoleErrorBoundary } from "@/components/shell/console-error-boundary";
import { SupportButton } from "@/components/shell/support-button";

function ConsoleShell({ children }: { children: React.ReactNode }) {
  const { projectId } = useProjectContext();
  const pathname = usePathname();
  const [paletteOpenSignal, setPaletteOpenSignal] = useState(0);
  // Mobile-only drawer state; on lg+ the sidebar is always visible.
  const [navOpen, setNavOpen] = useState(false);

  // Navigating (tapping a sidebar link) closes the drawer.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setNavOpen(false);
  }, [pathname]);

  useEffect(() => {
    if (!navOpen) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setNavOpen(false);
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [navOpen]);

  return (
    <div className="flex h-dvh flex-col overflow-hidden">
      <TopBar
        onOpenPalette={() => setPaletteOpenSignal((n) => n + 1)}
        onToggleNav={projectId ? () => setNavOpen((o) => !o) : undefined}
        navOpen={navOpen}
      />
      <div className="relative flex flex-1 overflow-hidden">
        {projectId && (
          <>
            {navOpen && (
              <div
                className="absolute inset-0 z-30 bg-black/50 lg:hidden"
                onClick={() => setNavOpen(false)}
                aria-hidden="true"
              />
            )}
            <aside
              className={`absolute inset-y-0 left-0 z-40 flex w-64 max-w-[85vw] shrink-0 flex-col bg-slate-900 shadow-2xl transition-transform duration-200 lg:static lg:w-60 lg:translate-x-0 lg:shadow-none ${
                navOpen ? "translate-x-0" : "-translate-x-full"
              }`}
            >
              <ProjectNav />
            </aside>
          </>
        )}
        <main className="flex-1 overflow-y-auto bg-white dark:bg-gray-950">
          <div className="p-4 sm:p-6 lg:p-8">
            <ConsoleErrorBoundary>{children}</ConsoleErrorBoundary>
          </div>
        </main>
      </div>
      {/* key forces the palette to mount/open when the top-bar button is clicked */}
      <CommandPalette key={paletteOpenSignal} initialOpen={paletteOpenSignal > 0} />
      <SupportButton />
    </div>
  );
}

export default function ConsoleLayout({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const { token, isLoading } = useAuth();

  useEffect(() => {
    if (!isLoading && !token) router.push("/login");
  }, [isLoading, token, router]);

  if (isLoading) {
    return (
      <div className="flex h-dvh items-center justify-center bg-gray-50">
        <Spinner size="lg" />
      </div>
    );
  }
  if (!token) return null;

  return (
    <Suspense
      fallback={
        <div className="flex h-dvh items-center justify-center bg-gray-50">
          <Spinner size="lg" />
        </div>
      }
    >
      <ThemeProvider>
        <ConsoleLangProvider>
          <ProjectProvider>
            <ConsoleShell>{children}</ConsoleShell>
          </ProjectProvider>
        </ConsoleLangProvider>
      </ThemeProvider>
    </Suspense>
  );
}
