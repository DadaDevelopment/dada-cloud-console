const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL ?? "";

const CLIENT_ERROR_PATH = "/api/v1/client-errors";

const recentByMessage = new Map<string, number>();

const THROTTLE_MS = 5000;

export type ClientErrorKind = "react" | "window" | "unhandledrejection";

export interface ClientErrorInput {
  message: string;
  stack?: string;
  componentStack?: string;
  url?: string;
  kind?: ClientErrorKind;
}

/**
 * Report a browser-side crash to the backend so it surfaces in server logs
 * (kubectl logs) instead of only the user's console. Best-effort: it throttles
 * repeats of the same message, prefers sendBeacon (survives page unload), and
 * never throws -- a failure to report a crash must not itself crash the app.
 */
export function reportClientError(input: ClientErrorInput): void {
  try {
    const message = (input.message || "").trim();
    if (!message) return;

    const now = Date.now();
    const key = message.slice(0, 200);
    const last = recentByMessage.get(key) ?? 0;
    if (now - last < THROTTLE_MS) return;
    recentByMessage.set(key, now);

    const href = input.url ?? (typeof location !== "undefined" ? location.href : "");
    const body = JSON.stringify({
      message: message.slice(0, 1000),
      stack: input.stack?.slice(0, 8000) ?? "",
      component_stack: input.componentStack?.slice(0, 4000) ?? "",
      url: href.slice(0, 500),
      kind: input.kind ?? "react",
    });

    const endpoint = `${API_BASE_URL}${CLIENT_ERROR_PATH}`;

    if (typeof navigator !== "undefined" && typeof navigator.sendBeacon === "function") {
      navigator.sendBeacon(endpoint, new Blob([body], { type: "application/json" }));
      return;
    }
    void fetch(endpoint, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
      keepalive: true,
    }).catch(() => {});
  } catch {
    return;
  }
}
