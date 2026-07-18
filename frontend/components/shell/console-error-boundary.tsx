"use client";

import { Component, type ReactNode } from "react";
import { usePathname } from "next/navigation";
import { useT } from "@/lib/i18n/console/context";
import { Button } from "@/components/ui/button";
import { reportClientError } from "@/lib/report-error";

const SUPPORT_EMAIL = "development@dada-tuda.ru";

function buildSupportMailto(subject: string, route: string, error: Error | null): string {
  const body = [
    `Route: ${route}`,
    `Error: ${error ? error.message : "unknown"}`,
  ].join("\n");
  return `mailto:${SUPPORT_EMAIL}?subject=${encodeURIComponent(subject)}&body=${encodeURIComponent(body)}`;
}

function CrashFallback({ error, onReload }: { error: Error | null; onReload: () => void }) {
  const { t } = useT();
  const pathname = usePathname();
  const mailto = buildSupportMailto(t("feedback.crash.mailSubject"), pathname ?? "unknown", error);

  return (
    <div className="flex min-h-[50vh] flex-col items-center justify-center gap-4 p-8 text-center">
      <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
        {t("feedback.crash.title")}
      </h2>
      <p className="max-w-md text-sm text-gray-600 dark:text-gray-400">
        {t("feedback.crash.body")}
      </p>
      <div className="flex flex-wrap items-center justify-center gap-3">
        <Button onClick={onReload}>{t("feedback.crash.reload")}</Button>
        <a
          href={mailto}
          className="text-sm font-medium text-blue-600 hover:underline dark:text-blue-400"
        >
          {t("feedback.crash.contactSupport")}
        </a>
      </div>
    </div>
  );
}

interface ConsoleErrorBoundaryState {
  error: Error | null;
}

/**
 * Catches render crashes anywhere in the console tree and shows a friendly
 * fallback instead of a blank white screen. Purely additive: wraps the
 * existing shell/children, never changes their normal render path.
 */
export class ConsoleErrorBoundary extends Component<{ children: ReactNode }, ConsoleErrorBoundaryState> {
  state: ConsoleErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): ConsoleErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, info: { componentStack: string }) {
    console.error("ConsoleErrorBoundary caught an error", error, info.componentStack);
    reportClientError({
      message: error?.message || String(error),
      stack: error?.stack,
      componentStack: info?.componentStack,
      kind: "react",
      url: typeof window !== "undefined" ? window.location.href : undefined,
    });
  }

  handleReload = () => {
    this.setState({ error: null });
    if (typeof window !== "undefined") window.location.reload();
  };

  render() {
    if (this.state.error) {
      return <CrashFallback error={this.state.error} onReload={this.handleReload} />;
    }
    return this.props.children;
  }
}
