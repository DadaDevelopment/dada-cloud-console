"use client";
import { createContext, useContext, useEffect, useState } from "react";
import type { User } from "./types";

interface AuthContextValue {
  user: User | null;
  token: string | null;
  login: (token: string, user: User) => void;
  logout: () => void;
  isLoading: boolean;
}

export const AuthContext = createContext<AuthContextValue | null>(null);

const AUTH_CHANGE_EVENT = "dada-auth-change";

function readAuthFromStorage() {
  const storedToken = localStorage.getItem("dada_token");
  const storedUser = localStorage.getItem("dada_user");
  if (!storedToken || !storedUser) {
    return { user: null, token: null, isLoading: false };
  }
  try {
    return {
      user: JSON.parse(storedUser) as User,
      token: storedToken,
      isLoading: false,
    };
  } catch {
    return { user: null, token: null, isLoading: false };
  }
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [auth, setAuth] = useState<{ user: User | null; token: string | null; isLoading: boolean }>(
    { user: null, token: null, isLoading: true },
  );

  useEffect(() => {
    // Initial hydration from localStorage. The state-from-storage pattern
    // is exactly what the rule's storage-event branch is for; the initial
    // read has to live in an effect because localStorage isn't available
    // during SSR. This is intentional, not the cascading-render footgun
    // the rule is guarding against.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setAuth(readAuthFromStorage());

    const handler = () => setAuth(readAuthFromStorage());
    window.addEventListener("storage", handler);
    window.addEventListener(AUTH_CHANGE_EVENT, handler as EventListener);
    return () => {
      window.removeEventListener("storage", handler);
      window.removeEventListener(AUTH_CHANGE_EVENT, handler as EventListener);
    };
  }, []);

  function login(newToken: string, newUser: User) {
    localStorage.setItem("dada_token", newToken);
    localStorage.setItem("dada_user", JSON.stringify(newUser));
    window.dispatchEvent(new Event(AUTH_CHANGE_EVENT));
  }

  function logout() {
    localStorage.removeItem("dada_token");
    localStorage.removeItem("dada_user");
    window.dispatchEvent(new Event(AUTH_CHANGE_EVENT));
  }

  return (
    <AuthContext.Provider value={{ ...auth, login, logout }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
