export interface DetectedSource {
  framework: string;
  port: number;
}

/**
 * A source-archive upload's detection result decides, right then, whether
 * the app will ever get an address: appNeedsDefaultDomain
 * (backend/internal/api/domains.go) never mints a hostname for a worker,
 * and isWorkerUpload (backend/internal/api/uploadsource.go) files an
 * upload as a worker whenever detection found no web framework and no
 * EXPOSE, i.e. `detected.port === 0`. Missing/non-finite/non-positive is
 * treated the same as 0 so a malformed response fails toward asking the
 * user rather than toward silently routing them past the build page --
 * see lib/app-next-step.ts for the live cost of staying silent about this
 * (yzfy@sls.xx.kg, two uploads, two dead-end workers, source downloaded
 * back and the app deleted).
 */
export function needsPortVerdict(detected: DetectedSource | null | undefined): boolean {
  return !detected || !Number.isFinite(detected.port) || detected.port <= 0;
}

/**
 * Parses a user-typed port the same way port-editor.tsx and
 * common-config-editor.tsx do: an integer strictly between 1 and 65535.
 * Returns null for anything else, including empty input, so callers can
 * treat null as "show the range error" without duplicating the range
 * check at each call site.
 */
export function parseUploadPort(raw: string): number | null {
  const port = Number(raw);
  if (!Number.isInteger(port) || port < 1 || port > 65535) return null;
  return port;
}

export interface UploadPortApiErrorLike {
  code?: string;
  message?: string;
}

export interface UploadPortErrorVerdict {
  isRangeError: boolean;
  message: string;
}

/**
 * Classifies a failed upload-port save the same way port-editor.tsx's
 * describeError does for the app-port PATCH: only the backend's own
 * `invalid_port` code claims the range error, and every other 4xx/5xx
 * shows the backend's own sentence verbatim. Branching on `err.message`
 * text instead of `err.code` is the exact bug that once told a user
 * rejected with `app_is_worker` that 8080 is out of range -- see
 * port-editor.tsx's describeError doc for the incident this guards
 * against.
 */
export function classifyUploadPortError(err: unknown): UploadPortErrorVerdict {
  const e = err as UploadPortApiErrorLike | undefined;
  return {
    isRangeError: e?.code === "invalid_port",
    message: e?.message ?? "",
  };
}
