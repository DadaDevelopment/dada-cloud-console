/**
 * What the user was trying to do when the plan gate stopped them.
 *
 * A checkout is a round trip through YooKassa's site, and the page that
 * started it does not survive it. Without this, someone who paid to install
 * Jellyfin comes back to a generic "payment received" screen and has to find
 * the catalog, the tile and the form again -- the exact spot where both of the
 * near-buyers we measured gave up. Carrying the intent across the redirect
 * turns the return screen into "you can finish now" with the way back.
 */
export interface UpgradeIntent {
  /** Where the action lives, e.g. /projects/<id>/apps. */
  returnTo: string;
  /** Quota that refused, e.g. storage_gb. Used to name the action on return. */
  resource: string;
  /** Plan key bought, for the confirmation line. */
  plan: string;
  /** Human label of the thing being created, when the surface knows one. */
  label?: string;
}

const STORAGE_KEY = "dada.upgrade-intent";

/** Minimal slice of Storage this module needs, so tests can pass a fake. */
export interface IntentStore {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

/**
 * Returns sessionStorage, or null where it does not exist (server render, or
 * a browser that refuses storage). Every caller degrades to "no intent", which
 * is the pre-existing behaviour rather than a crash.
 */
export function defaultIntentStore(): IntentStore | null {
  try {
    if (typeof sessionStorage === "undefined") return null;
    return sessionStorage;
  } catch {
    return null;
  }
}

/** Remembers the intent for the duration of the checkout round trip. */
export function saveUpgradeIntent(intent: UpgradeIntent, store: IntentStore | null = defaultIntentStore()): void {
  if (!store) return;
  try {
    store.setItem(STORAGE_KEY, JSON.stringify(intent));
  } catch {
    return;
  }
}

/**
 * Reads and clears the intent. Clearing on read is deliberate: an intent that
 * outlives its checkout would send a user back to a form they already
 * submitted, days later, for a reason they no longer remember.
 */
export function takeUpgradeIntent(store: IntentStore | null = defaultIntentStore()): UpgradeIntent | null {
  if (!store) return null;
  let raw: string | null = null;
  try {
    raw = store.getItem(STORAGE_KEY);
    store.removeItem(STORAGE_KEY);
  } catch {
    return null;
  }
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Partial<UpgradeIntent>;
    if (!parsed || typeof parsed.returnTo !== "string" || !parsed.returnTo.startsWith("/")) return null;
    if (parsed.returnTo.startsWith("//")) return null;
    return {
      returnTo: parsed.returnTo,
      resource: typeof parsed.resource === "string" ? parsed.resource : "",
      plan: typeof parsed.plan === "string" ? parsed.plan : "",
      label: typeof parsed.label === "string" ? parsed.label : undefined,
    };
  } catch {
    return null;
  }
}
