"use client";
import { useEffect, useRef, useState } from "react";

/**
 * useInViewport reports whether an element has entered (or come near) the
 * viewport at least once, via an IntersectionObserver with a generous rootMargin
 * so charts mount just before they scroll into view. It latches true and stops
 * observing — charts stay mounted once seen so their zoom/pan state survives
 * scroll-away. This is the hook behind panel virtualization: on a board with
 * dozens of panels, only the visible/near ones ever instantiate an ECharts
 * canvas. Falls back to true when IntersectionObserver is unavailable (SSR-safe:
 * starts false, resolves on mount).
 */
export function useInViewport<T extends Element>(rootMargin = "300px"): {
  ref: React.RefObject<T | null>;
  seen: boolean;
} {
  const ref = useRef<T | null>(null);
  const [seen, setSeen] = useState(false);

  useEffect(() => {
    if (seen) return;
    const el = ref.current;
    if (!el) return;
    if (typeof IntersectionObserver === "undefined") {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setSeen(true);
      return;
    }
    const obs = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setSeen(true);
          obs.disconnect();
        }
      },
      { rootMargin },
    );
    obs.observe(el);
    return () => obs.disconnect();
  }, [seen, rootMargin]);

  return { ref, seen };
}
