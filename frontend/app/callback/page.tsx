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
import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { Spinner } from "@/components/ui/spinner";

export default function CallbackPage() {
  const router = useRouter();
  const { token, isLoading } = useAuth();

  useEffect(() => {
    if (!isLoading && token) {
      const target = window.location.pathname + window.location.search;
      router.replace(target === "/callback" ? "/projects" : target);
    }
  }, [isLoading, token, router]);

  return (
    <div className="flex h-screen items-center justify-center bg-gray-50">
      <Spinner size="lg" />
    </div>
  );
}
