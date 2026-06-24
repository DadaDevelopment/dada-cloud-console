import type { MetadataRoute } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";

// Marketing routes only. The console (console.dada-tuda.ru) is a separate host
// and is not part of this public sitemap.
const ROUTES: Array<{ path: string; priority: number; changeFrequency: MetadataRoute.Sitemap[number]["changeFrequency"] }> = [
  { path: "/", priority: 1.0, changeFrequency: "weekly" },
  { path: "/pricing", priority: 0.9, changeFrequency: "weekly" },
  { path: "/cloud-servers", priority: 0.8, changeFrequency: "monthly" },
  { path: "/databases", priority: 0.8, changeFrequency: "monthly" },
  { path: "/kubernetes", priority: 0.8, changeFrequency: "monthly" },
  { path: "/storage", priority: 0.8, changeFrequency: "monthly" },
  { path: "/developer", priority: 0.7, changeFrequency: "monthly" },
];

export default function sitemap(): MetadataRoute.Sitemap {
  const lastModified = new Date();
  return ROUTES.map(({ path, priority, changeFrequency }) => ({
    url: `${SITE_URL}${path}`,
    lastModified,
    changeFrequency,
    priority,
  }));
}
