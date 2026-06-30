"use client";
import { Suspense, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { Spinner } from "@/components/ui/spinner";
import { ProjectProvider, useProjectContext } from "@/lib/project-context";
import { ConsoleLangProvider } from "@/lib/i18n/console/context";
import { ThemeProvider } from "@/lib/theme/context";
import { TopBar } from "@/components/shell/top-bar";
import { ProjectNav } from "@/components/shell/project-nav";
import { CommandPalette } from "@/components/shell/command-palette";

function ConsoleShell({ children }: { children: React.ReactNode }) {
  const { projectId } = useProjectContext();
  const [paletteOpenSignal, setPaletteOpenSignal] = useState(0);

  return (
    <div className="flex h-screen flex-col overflow-hidden">
      <TopBar onOpenPalette={() => setPaletteOpenSignal((n) => n + 1)} />
      <div className="flex flex-1 overflow-hidden">
        {projectId && (
          <aside className="flex w-60 shrink-0 flex-col bg-slate-900">
            <ProjectNav />
          </aside>
        )}
        <main className="flex-1 overflow-y-auto bg-white dark:bg-gray-950">
          <div className="p-8">{children}</div>
        </main>
      </div>
      {/* key forces the palette to mount/open when the top-bar button is clicked */}
      <CommandPalette key={paletteOpenSignal} initialOpen={paletteOpenSignal > 0} />
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
      <div className="flex h-screen items-center justify-center bg-gray-50">
        <Spinner size="lg" />
      </div>
    );
  }
  if (!token) return null;

  return (
    <Suspense
      fallback={
        <div className="flex h-screen items-center justify-center bg-gray-50">
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
