"use client";
import { Suspense, useEffect, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { Spinner } from "@/components/ui/spinner";
import { AuthErrorScreen } from "@/components/shell/auth-error-screen";
import { ProjectProvider, useProjectContext } from "@/lib/project-context";
import { ConsoleLangProvider } from "@/lib/i18n/console/context";
import { ThemeProvider } from "@/lib/theme/context";
import { TopBar } from "@/components/shell/top-bar";
import { ProjectNav } from "@/components/shell/project-nav";
import { GraceBanner } from "@/components/shell/grace-banner";
import { CommandPalette } from "@/components/shell/command-palette";
import { ConsoleErrorBoundary } from "@/components/shell/console-error-boundary";
import { GlobalErrorReporter } from "@/components/shell/global-error-reporter";
import { SupportButton } from "@/components/shell/support-button";
import { DocumentTitle } from "@/components/shell/document-title";
import { BuildWatcher } from "@/components/shell/build-watcher";
import { AgentChatPanel } from "@/components/agent-chat-panel";
import { OnboardingProvider } from "@/components/onboarding/onboarding-provider";
import { PasskeyPrompt } from "@/components/passkey/passkey-prompt";
import { useT } from "@/lib/i18n/console/context";
import { AgentPetButton } from "@/components/shell/agent-pet-button";

function ConsoleShell({ children }: { children: React.ReactNode }) {
  const { projectId } = useProjectContext();
  const { t } = useT();
  const pathname = usePathname();
  const [paletteOpenSignal, setPaletteOpenSignal] = useState(0);
  // Mobile-only drawer state; on lg+ the sidebar is always visible.
  const [navOpen, setNavOpen] = useState(false);
  // Agent chat panel is collapsed by default on every viewport.
  const [chatOpen, setChatOpen] = useState(false);
  const [passkeyOpen, setPasskeyOpen] = useState(false);

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

  useEffect(() => {
    function checkAgentHash() {
      if (window.location.hash === "#agent") setChatOpen(true);
    }
    checkAgentHash();
    window.addEventListener("hashchange", checkAgentHash);
    return () => window.removeEventListener("hashchange", checkAgentHash);
  }, []);

  return (
    <div className="flex h-dvh flex-col overflow-hidden">
      <DocumentTitle />
      <BuildWatcher />
      <TopBar
        onOpenPalette={() => setPaletteOpenSignal((n) => n + 1)}
        onToggleNav={projectId ? () => setNavOpen((o) => !o) : undefined}
        navOpen={navOpen}
      />
      <GraceBanner />
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
        {chatOpen && (
          <div
            className="absolute inset-0 z-30 bg-black/50 lg:hidden"
            onClick={() => setChatOpen(false)}
            aria-hidden="true"
          />
        )}
        {!chatOpen && (
          <AgentPetButton
            dataOnboarding="agent-fab"
            onClick={() => setChatOpen(true)}
            title={t("agentChat.open")}
            ariaLabel={t("agentChat.open")}
          />
        )}
        <AgentChatPanel open={chatOpen} onClose={() => setChatOpen(false)} />
        <OnboardingProvider suppressed={chatOpen || navOpen || passkeyOpen} />
        <PasskeyPrompt onOpenChange={setPasskeyOpen} />
      </div>
      {/* key forces the palette to mount/open when the top-bar button is clicked */}
      <CommandPalette key={paletteOpenSignal} initialOpen={paletteOpenSignal > 0} />
      <SupportButton />
    </div>
  );
}

/**
 * Sits between {@link ProjectProvider} and the console shell so two distinct
 * bootstrap failures never render as a blank console with zero signal:
 *   - a `signup_closed` 403 on the first authenticated request - see
 *     {@link useProjectContext}'s `signupClosed` - renders the same dead-end
 *     treatment as an `authError`, with no retry (the account genuinely
 *     cannot be provisioned right now).
 *   - a failed one-shot default-project provisioning for a fresh,
 *     zero-project account - see `bootstrapError` - renders the same
 *     dead-end shape but WITH a retry, wired to `retryBootstrap`.
 * Both used to fall through to a shell with an empty project switcher.
 */
function ConsoleGate({ children }: { children: React.ReactNode }) {
  const { signupClosed, bootstrapError, retryBootstrap } = useProjectContext();
  const { logout } = useAuth();

  if (signupClosed) {
    return (
      <AuthErrorScreen variant="signupClosed" onRetry={() => window.location.reload()} onLogout={logout} />
    );
  }

  if (bootstrapError) {
    return <AuthErrorScreen variant="bootstrapFailed" onRetry={retryBootstrap} onLogout={logout} />;
  }

  return (
    <>
      <GlobalErrorReporter />
      <ConsoleShell>{children}</ConsoleShell>
    </>
  );
}

export default function ConsoleLayout({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const { token, isLoading, authError, logout } = useAuth();

  useEffect(() => {
    if (!isLoading && !token && !authError) router.push("/login");
  }, [isLoading, token, authError, router]);

  if (authError) {
    return <AuthErrorScreen onRetry={() => window.location.reload()} onLogout={logout} />;
  }

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
            <ConsoleGate>{children}</ConsoleGate>
          </ProjectProvider>
        </ConsoleLangProvider>
      </ThemeProvider>
    </Suspense>
  );
}
