import type { NextConfig } from "next";
import path from "path";

/**
 * Landing slugs that were renamed after Yandex had already crawled them. The old
 * paths still sit in the crawl queue and answer 404, which burns crawl budget and
 * drops whatever authority the old URL had earned. 301 rather than the 308 that
 * `permanent: true` emits, because some older crawlers still treat 308 as unknown.
 */
const RENAMED_LANDINGS: [string, string][] = [
  ["/telegram-bot-hosting", "/hosting-telegram-bot"],
  ["/vibe-coding-deploy", "/deploy-vibe-coding"],
];

const nextConfig: NextConfig = {
  output: "standalone",
  transpilePackages: ["@dada/react-sso"],
  turbopack: {
    root: path.resolve(__dirname),
  },
  async redirects() {
    return RENAMED_LANDINGS.flatMap(([from, to]) => [
      { source: from, destination: to, statusCode: 301 },
      { source: `/en${from}`, destination: `/en${to}`, statusCode: 301 },
    ]);
  },
};

export default nextConfig;
