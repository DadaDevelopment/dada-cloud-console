"use client";

import { useEffect } from "react";
import { usePathname } from "next/navigation";

import { startUxTelemetry, trackPageview, flushUxEvents } from "@/lib/ux-telemetry";

/**
 * Mounts client-side UX telemetry: global click capture plus one `pageview` per
 * route change. Renders nothing.
 *
 * Mount it as high as possible (root layout) so the pre-login part of the path
 * is captured too -- that is the half `audit_events` can never see. See
 * lib/ux-telemetry.ts for the privacy rules and the fail-open contract.
 */
export function UxTelemetryProvider(): null {
  const pathname = usePathname();

  useEffect(() => {
    startUxTelemetry();
    return () => {
      flushUxEvents();
    };
  }, []);

  useEffect(() => {
    trackPageview(pathname ?? "");
  }, [pathname]);

  return null;
}
