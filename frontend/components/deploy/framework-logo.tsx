import {
  siApachemaven,
  siDjango,
  siDocker,
  siExpress,
  siFastapi,
  siFastify,
  siFlask,
  siGo,
  siGradle,
  siNestjs,
  siNextdotjs,
  siNodedotjs,
  siNuxt,
  siPython,
  siReact,
  siRemix,
  siScala,
  siSpring,
  siSvelte,
  siVite,
  type SimpleIcon,
} from "simple-icons";
import { Globe } from "lucide-react";

/**
 * Brand logos for framework presets shown in the git-import picker.
 *
 * Icons come from simple-icons (single-path, monochrome brand marks). Near-black
 * marks are rendered with currentColor so they stay visible in dark mode; the
 * rest use the official brand hex.
 */

const PRESET_ICON: Record<string, SimpleIcon> = {
  "spring-maven": siSpring,
  "spring-gradle": siSpring,
  "spring-boot": siSpring,
  scala: siScala,
  maven: siApachemaven,
  gradle: siGradle,
  go: siGo,
  fastapi: siFastapi,
  flask: siFlask,
  django: siDjango,
  python: siPython,
  nextjs: siNextdotjs,
  nuxt: siNuxt,
  sveltekit: siSvelte,
  svelte: siSvelte,
  react: siReact,
  nestjs: siNestjs,
  express: siExpress,
  fastify: siFastify,
  node: siNodedotjs,
  vite: siVite,
  remix: siRemix,
  dockerfile: siDocker,
};

/** True when the brand hex is dark enough to disappear on a dark surface. */
function isDark(hex: string): boolean {
  const n = parseInt(hex, 16);
  const r = (n >> 16) & 0xff;
  const g = (n >> 8) & 0xff;
  const b = n & 0xff;
  return 0.2126 * r + 0.7152 * g + 0.0722 * b < 70;
}

export function FrameworkLogo({ id, className }: { id: string | null | undefined; className?: string }) {
  const size = className ?? "h-5 w-5";
  if (!id) return null;
  if (id === "static") return <Globe aria-hidden className={`${size} text-gray-400 dark:text-gray-500`} />;

  const icon = PRESET_ICON[id];
  if (!icon) {
    return (
      <span
        aria-hidden
        className={`inline-flex shrink-0 items-center justify-center rounded bg-gray-500 text-[0.55rem] font-semibold text-white ${size}`}
      >
        {id.slice(0, 2)}
      </span>
    );
  }

  const dark = isDark(icon.hex);
  return (
    <svg
      role="img"
      aria-label={icon.title}
      viewBox="0 0 24 24"
      className={`shrink-0 ${size} ${dark ? "fill-gray-800 dark:fill-gray-100" : ""}`}
      fill={dark ? undefined : `#${icon.hex}`}
    >
      <path d={icon.path} />
    </svg>
  );
}
