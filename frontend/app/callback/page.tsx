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
import { PENDING_REGISTRATION_KEY, readCompletedRegistration } from "@/lib/register-redirect";
import { capturePasskeyActionStatus, markFreshAuthentication } from "@/lib/passkey";
import { GOAL_AUTH_CALLBACK_FAILED, GOAL_REGISTRATION_COMPLETE, reachGoal } from "@/lib/metrika";
import { callbackDiagnostics, callbackVerdict } from "@/lib/callback-outcome";

/**
 * Reads a browser store without letting a blocked one (private mode, storage
 * disabled by policy) throw out of the failure-reporting path. The diagnostics
 * treat an absent store as unreadable, which is itself the finding.
 */
function safeStore(get: () => Storage): Storage | null {
  try {
    return get();
  } catch {
    return null;
  }
}

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

/**
 * The query string Keycloak redirected to, snapshotted at module scope for
 * the same reason as the passkey status above: react-sso rewrites the URL on
 * the success path. Empty on the server, which is harmless - auth always
 * starts out loading, so the first render is the spinner on both sides and
 * the failure verdict is only ever reached in the browser.
 */
const CALLBACK_SEARCH = typeof window !== "undefined" ? window.location.search : "";

export default function CallbackPage() {
  const router = useRouter();
  const { token, isLoading, authError, login, logout } = useAuth();
  const registrationGoalCheckedRef = useRef(false);
  const failureGoalSentRef = useRef(false);

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
      const method = readCompletedRegistration(pending, Date.now(), REGISTRATION_WINDOW_MS);
      if (!method) return;
      reachGoal(GOAL_REGISTRATION_COMPLETE, { method });
    } catch {}
  }, [isLoading, token]);

  /**
   * A round-trip that comes back without a session is the console's quietest
   * way to lose a new user: no token, no error, nothing to retry, and no
   * authenticated request afterwards - so not even the audit log records that
   * they were ever here. Count it, so the drop-off stops being invisible.
   */
  useEffect(() => {
    if (isLoading || token || failureGoalSentRef.current) return;
    failureGoalSentRef.current = true;
    const outcome = callbackVerdict({
      isLoading,
      hasToken: false,
      hasAuthError: authError !== null,
      search: CALLBACK_SEARCH,
    });
    if (outcome.state !== "failed") return;
    const diagnostics = callbackDiagnostics(CALLBACK_SEARCH, [
      safeStore(() => window.sessionStorage),
      safeStore(() => window.localStorage),
    ]);
    reachGoal(GOAL_AUTH_CALLBACK_FAILED, {
      reason: outcome.error ?? outcome.reason,
      has_code: String(diagnostics.has_code),
      has_state: String(diagnostics.has_state),
      state_entry: diagnostics.state_entry,
      oidc_keys: String(diagnostics.oidc_keys),
    });
  }, [isLoading, token, authError]);

  const verdict = callbackVerdict({
    isLoading,
    hasToken: token !== null,
    hasAuthError: authError !== null,
    search: CALLBACK_SEARCH,
  });

  if (verdict.state === "failed") {
    /**
     * Retry restarts the authorize request instead of reloading: the code in
     * this URL is either absent or already spent, so a reload would land on
     * the very same failure.
     */
    return (
      <AuthErrorScreen
        variant={authError !== null ? "session" : verdict.reason}
        onRetry={authError !== null ? () => window.location.reload() : () => login()}
        onLogout={logout}
      />
    );
  }

  return (
    <div className="flex h-screen items-center justify-center bg-gray-50">
      <Spinner size="lg" />
    </div>
  );
}
