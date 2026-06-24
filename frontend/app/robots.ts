import type { MetadataRoute } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";

export default function robots(): MetadataRoute.Robots {
  return {
    rules: {
      userAgent: "*",
      allow: "/",
      // Console surfaces live on console.dada-tuda.ru, but guard the paths here
      // too in case they are ever reachable on the marketing host.
      disallow: ["/projects", "/admin", "/ai-studio", "/callback", "/login"],
    },
    sitemap: `${SITE_URL}/sitemap.xml`,
    host: SITE_URL,
  };
}
