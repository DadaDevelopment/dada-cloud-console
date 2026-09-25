import { useMemo } from "react";
import { blobatarUri } from "blobatar/uri";

/**
 * Deterministic generated avatar for a user. The seed should be a stable
 * identifier (email, then username, then id) so the picture does not change
 * when the display name is edited.
 */
export function UserAvatar({ seed, size = 32, className = "" }: { seed: string; size?: number; className?: string }) {
  const src = useMemo(() => blobatarUri(seed.trim().toLowerCase() || "anonymous"), [seed]);
  return <img src={src} alt="" aria-hidden width={size} height={size} className={`shrink-0 rounded-full ${className}`} />;
}
