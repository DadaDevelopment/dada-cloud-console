import type { MetadataRoute } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";

// Marketing routes only. The console (console.dada-tuda.ru) is a separate host
// and is not part of this public sitemap. RU is served at the root, EN at the
// "/en" prefix; each entry advertises both via hreflang alternates.
const ROUTES: Array<{ path: string; priority: number; changeFrequency: MetadataRoute.Sitemap[number]["changeFrequency"] }> = [
  { path: "/", priority: 1.0, changeFrequency: "weekly" },
  { path: "/pricing", priority: 0.9, changeFrequency: "weekly" },
  { path: "/cloud-servers", priority: 0.8, changeFrequency: "monthly" },
  { path: "/databases", priority: 0.8, changeFrequency: "monthly" },
  { path: "/storage", priority: 0.8, changeFrequency: "monthly" },
  { path: "/developer", priority: 0.7, changeFrequency: "monthly" },
];

const ruUrl = (path: string) => `${SITE_URL}${path}`;
const enUrl = (path: string) => `${SITE_URL}${path === "/" ? "/en" : `/en${path}`}`;

export default function sitemap(): MetadataRoute.Sitemap {
  const lastModified = new Date();
  return ROUTES.flatMap(({ path, priority, changeFrequency }) => {
    const languages = { "ru-RU": ruUrl(path), "en-US": enUrl(path), "x-default": ruUrl(path) };
    return [
      { url: ruUrl(path), lastModified, changeFrequency, priority, alternates: { languages } },
      { url: enUrl(path), lastModified, changeFrequency, priority: priority * 0.9, alternates: { languages } },
    ];
  });
}
