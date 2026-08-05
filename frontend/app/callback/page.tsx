"use client";
/**
 * OIDC redirect callback. `@dada/react-sso`'s oidcProvider.load() handles
 * signinRedirectCallback automatically when it detects code= in the URL,
 * replacing the URL with the returnTo path carried in the OIDC `state` (via
 * window.history.replaceState) before flipping to authenticated. That history
 * change lands on this same route component without a client navigation, so
 * once authenticated this reads the URL the library already rewrote and
 * navigates there; falls back to /projects for the redirect_uri's own
 * pathname (no returnTo was set).
 */
import { useEffect, useRef } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { Spinner } from "@/components/ui/spinner";
import { AuthErrorScreen } from "@/components/shell/auth-error-screen";
import { PENDING_REGISTRATION_KEY } from "@/lib/register-redirect";
import { capturePasskeyActionStatus, markFreshAuthentication } from "@/lib/passkey";
import { GOAL_REGISTRATION_COMPLETE, reachGoal } from "@/lib/metrika";

/**
 * Keycloak reports the outcome of an Application-Initiated Action (passkey
 * enrollment) as `?kc_action_status=…` on this redirect_uri. Snapshot it at
 * module scope, before react-sso's `load()` rewrites the URL and before this
 * route navigates on, so the console can record the result.
 *
 * Landing here also means an authorize round-trip just closed, which is the
 * only moment the passkey offer may appear at all. Whether the user actually
 * typed anything (versus being waved through by the session cookie) is decided
 * separately from the token's `auth_time`.
 */
if (typeof window !== "undefined") {
  capturePasskeyActionStatus();
  markFreshAuthentication();
}

/**
 * How long a {@link PENDING_REGISTRATION_KEY} marker stays valid. Bounds the
 * window so a stale marker (user abandoned sign-up, came back days later via
 * a plain login) can't misfire `registration_complete`.
 */
const REGISTRATION_WINDOW_MS = 30 * 60 * 1000;

export default function CallbackPage() {
  const router = useRouter();
  const { token, isLoading, authError, logout } = useAuth();
  const registrationGoalCheckedRef = useRef(false);

  useEffect(() => {
    if (!isLoading && token) {
      const target = window.location.pathname + window.location.search;
      router.replace(target === "/callback" ? "/projects" : target);
    }
  }, [isLoading, token, router]);

  /**
   * Fires the `registration_complete` Metrika goal the moment a Keycloak
   * sign-up actually completes — the existing `register` goal only measures
   * reaching /register (intent), so it overcounts against real signups.
   * Guarded by consuming {@link PENDING_REGISTRATION_KEY}, which `startRegister`
   * sets right before the redirect: only present here if this authenticated
   * return came from the sign-up flow, and only honored within
   * {@link REGISTRATION_WINDOW_MS} of being set.
   */
  useEffect(() => {
    if (isLoading || !token) return;
    if (registrationGoalCheckedRef.current) return;
    registrationGoalCheckedRef.current = true;
    try {
      const pending = window.localStorage.getItem(PENDING_REGISTRATION_KEY);
      window.localStorage.removeItem(PENDING_REGISTRATION_KEY);
      if (!pending) return;
      const startedAt = Number(pending);
      if (!Number.isFinite(startedAt) || Date.now() - startedAt > REGISTRATION_WINDOW_MS) return;
      reachGoal(GOAL_REGISTRATION_COMPLETE);
    } catch {}
  }, [isLoading, token]);

  if (authError) {
    return <AuthErrorScreen onRetry={() => window.location.reload()} onLogout={logout} />;
  }

  return (
    <div className="flex h-screen items-center justify-center bg-gray-50">
      <Spinner size="lg" />
    </div>
  );
}
