"use client";
// OIDC redirect callback — oidcProvider.load() handles signinRedirectCallback
// automatically when it detects code= in the URL (replaces URL with returnTo state).
// Once authenticated, redirect to /projects.
import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { Spinner } from "@/components/ui/spinner";

export default function CallbackPage() {
  const router = useRouter();
  const { token, isLoading } = useAuth();

  useEffect(() => {
    if (!isLoading && token) {
      router.replace("/projects");
    }
  }, [isLoading, token, router]);

  return (
    <div className="flex h-screen items-center justify-center bg-gray-50">
      <Spinner size="lg" />
    </div>
  );
}
