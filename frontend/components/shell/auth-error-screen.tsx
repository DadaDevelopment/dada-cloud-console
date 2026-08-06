"use client";

import { Button } from "@/components/ui/button";
import { ConsoleLangProvider, useT } from "@/lib/i18n/console/context";

/**
 * Which flavour of dead end is on screen. `session` is the default: auth
 * settled into an error while the console was already carrying the user.
 * `denied` and `callback` belong to the /callback route, where the failure
 * happened inside the authorize round-trip and the honest next step is to
 * start sign-in over rather than reload a spent redirect URL.
 */
export type AuthErrorVariant = "session" | "denied" | "callback";

const BODY_KEY: Record<AuthErrorVariant, string> = {
  session: "authError.body",
  denied: "authError.body.denied",
  callback: "authError.body.callback",
};

interface AuthErrorScreenProps {
  onRetry: () => void;
  onLogout: () => void;
  variant?: AuthErrorVariant;
}

function AuthErrorContent({ onRetry, onLogout, variant = "session" }: AuthErrorScreenProps) {
  const { t } = useT();
  return (
    <div className="flex h-dvh flex-col items-center justify-center gap-4 bg-gray-50 p-8 text-center dark:bg-gray-950">
      <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("authError.title")}</h2>
      <p className="max-w-md text-sm text-gray-600 dark:text-gray-400">{t(BODY_KEY[variant])}</p>
      <div className="flex flex-wrap items-center justify-center gap-3">
        <Button onClick={onRetry}>{t(variant === "session" ? "authError.retry" : "authError.retryLogin")}</Button>
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
export function AuthErrorScreen({ onRetry, onLogout, variant }: AuthErrorScreenProps) {
  return (
    <ConsoleLangProvider>
      <AuthErrorContent onRetry={onRetry} onLogout={onLogout} variant={variant} />
    </ConsoleLangProvider>
  );
}
