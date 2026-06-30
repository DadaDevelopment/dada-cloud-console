import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

/**
 * cn merges conditional class names (clsx) and dedupes conflicting Tailwind
 * utilities (tailwind-merge), so callers can layer base + variant + override
 * classes without worrying about which "p-2 / p-4" wins.
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
