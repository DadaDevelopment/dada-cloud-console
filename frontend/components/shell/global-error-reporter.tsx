"use client";

import { useEffect } from "react";
import { reportClientError } from "@/lib/report-error";

/**
 * Registers window-level handlers so uncaught errors and unhandled promise
 * rejections (the ones React error boundaries never see) are also reported to
 * the backend log sink. Renders nothing; mount once inside the console shell.
 */
export function GlobalErrorReporter() {
  useEffect(() => {
    function onError(e: ErrorEvent) {
      reportClientError({
        message: e.message || "window error",
        stack: e.error?.stack,
        kind: "window",
      });
    }
    function onRejection(e: PromiseRejectionEvent) {
      const reason = e.reason;
      reportClientError({
        message: reason?.message || String(reason) || "unhandledrejection",
        stack: reason?.stack,
        kind: "unhandledrejection",
      });
    }
    window.addEventListener("error", onError);
    window.addEventListener("unhandledrejection", onRejection);
    return () => {
      window.removeEventListener("error", onError);
      window.removeEventListener("unhandledrejection", onRejection);
    };
  }, []);

  return null;
}
