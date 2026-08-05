// Auth helpers — re-exports context/provider from auth-provider.tsx (JSX lives there)
// and provides standalone token utilities.

export { AuthProvider, useAuth, AuthContext } from "./auth-provider";
export type { AuthErrorCode } from "./auth-provider";

export function getStoredToken(): string | null {
  if (typeof window === "undefined") return null;
  return localStorage.getItem("dada_token");
}
