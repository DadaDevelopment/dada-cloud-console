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
  ["/kubernetes", "/cloud-servers"],
];

/**
 * Brand hosts that must consolidate onto the canonical marketing host. The
 * ingress can only 301 to a literal URL, so every apex path collapsed onto the
 * homepage and `dada-tuda.ru/robots.txt` answered with the landing page's HTML.
 * Redirecting here keeps the path, which is what makes an inbound link to an
 * apex URL worth anything.
 *
 * Inert until the apex ingress stops redirecting and routes to this service --
 * ship this side first so the two never overlap.
 */
const BRAND_HOSTS = ["dada-tuda.ru", "www.dada-tuda.ru"];
const CANONICAL_HOST = "https://cloud.dada-tuda.ru";

const nextConfig: NextConfig = {
  output: "standalone",
  transpilePackages: ["@dada/react-sso"],
  turbopack: {
    root: path.resolve(__dirname),
  },
  async redirects() {
    return [
      ...BRAND_HOSTS.map((host) => ({
        source: "/:path*",
        has: [{ type: "host" as const, value: host }],
        destination: `${CANONICAL_HOST}/:path*`,
        statusCode: 301,
      })),
      ...RENAMED_LANDINGS.flatMap(([from, to]) => [
        { source: from, destination: to, statusCode: 301 },
        { source: `/en${from}`, destination: `/en${to}`, statusCode: 301 },
      ]),
    ];
  },
};

export default nextConfig;
