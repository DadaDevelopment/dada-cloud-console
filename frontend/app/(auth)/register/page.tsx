"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

/**
 * Compatibility endpoint for legacy links. The identity provider's login page
 * is the only entry point for both native sign-in/sign-up and Yandex; retain
 * query parameters so attribution and an optional return path survive.
 */
export default function RegisterPage() {
  const router = useRouter();

  useEffect(() => {
    const query = window.location.search.slice(1);
    router.replace(`/login${query ? `?${query}` : ""}`);
  }, [router]);

  return <div className="min-h-screen bg-gray-50" />;
}
