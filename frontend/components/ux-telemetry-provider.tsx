"use client";

import { useEffect } from "react";
import { usePathname } from "next/navigation";

import { startUxTelemetry, trackPageview, flushUxEvents } from "@/lib/ux-telemetry";
import { rememberAttribution } from "@/lib/metrika";

/**
 * Mounts client-side UX telemetry: global click capture plus one `pageview` per
 * route change. Renders nothing.
 *
 * Mount it as high as possible (root layout) so the pre-login part of the path
 * is captured too -- that is the half `audit_events` can never see. See
 * lib/ux-telemetry.ts for the privacy rules and the fail-open contract.
 *
 * Also fires `rememberAttribution` once per page load, here rather than inside
 * lib/ux-telemetry.ts, so the visitor's first touch (utm/referrer) is captured
 * on whatever page they land on -- not just `/register`, which sees them only
 * after they have already clicked several internal links. See
 * lib/metrika.ts for the cookie contract the backend reads at signup.
 */
export function UxTelemetryProvider(): null {
  const pathname = usePathname();

  useEffect(() => {
    startUxTelemetry();
    rememberAttribution();
    return () => {
      flushUxEvents();
    };
  }, []);

  useEffect(() => {
    trackPageview(pathname ?? "");
  }, [pathname]);

  return null;
}
