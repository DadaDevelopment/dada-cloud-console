"use client";

import { Button } from "@/components/ui/button";
import { ConsoleLangProvider, useT } from "@/lib/i18n/console/context";

interface AuthErrorScreenProps {
  onRetry: () => void;
  onLogout: () => void;
}

function AuthErrorContent({ onRetry, onLogout }: AuthErrorScreenProps) {
  const { t } = useT();
  return (
    <div className="flex h-dvh flex-col items-center justify-center gap-4 bg-gray-50 p-8 text-center dark:bg-gray-950">
      <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("authError.title")}</h2>
      <p className="max-w-md text-sm text-gray-600 dark:text-gray-400">{t("authError.body")}</p>
      <div className="flex flex-wrap items-center justify-center gap-3">
        <Button onClick={onRetry}>{t("authError.retry")}</Button>
        <Button variant="outline" onClick={onLogout}>
          {t("authError.logout")}
        </Button>
      </div>
    </div>
  );
}

/**
 * Dead-end screen shown in place of the console spinner when
 * `useAuth().authError` is set: the access-token fetch exhausted its
 * retries, the OIDC provider chunk failed to load, or the loading watchdog
 * fired. Renders before the console shell mounts, so it is not inside
 * {@link ConsoleLangProvider} yet and brings its own instance rather than
 * depending on one that has not been reached.
 *
 * Callers must not redirect to /login on `authError`: that route
 * auto-starts a Keycloak redirect for a logged-out user, and under a live
 * SSO session Keycloak would bounce the user straight back into the same
 * failure, producing an infinite redirect loop instead of this screen.
 */
export function AuthErrorScreen({ onRetry, onLogout }: AuthErrorScreenProps) {
  return (
    <ConsoleLangProvider>
      <AuthErrorContent onRetry={onRetry} onLogout={onLogout} />
    </ConsoleLangProvider>
  );
}
