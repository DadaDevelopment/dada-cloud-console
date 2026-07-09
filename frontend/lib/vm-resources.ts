import type { ResourceSnapshot } from "@/lib/types";

/**
 * Classification + field extraction for VM (compose) resources.
 *
 * A compose environment holds first-class Resources — an Ingress (nginx routing),
 * a ServiceDatabase (managed postgres/redis/…) — alongside ordinary Applications.
 * The backend marks purpose-built ones with a top-level `managed` key in
 * summary_json; adopted ones lack it and are classified by image. The console
 * renders each type with its own detail view instead of a raw compose block.
 */

export type VMResourceType = "ingress" | "database" | "app";

const DB_ENGINES = new Set([
  "postgres",
  "postgresql",
  "mysql",
  "mariadb",
  "mongo",
  "mongodb",
  "redis",
  "valkey",
  "memcached",
  "clickhouse",
]);

const DB_IMAGE_RE = /(?:^|\/)(postgres|postgresql|mysql|mariadb|mongo|mongodb|redis|valkey|memcached|clickhouse)(?::|$)/i;
const PROXY_IMAGE_RE = /(?:^|\/)(nginx|traefik|haproxy|caddy|envoy)(?::|$)/i;

interface ComposeBlock {
  image?: string;
  ports?: string[];
  volumes?: string[];
  environment?: Record<string, string>;
}

interface VMSummary {
  image?: string;
  runtime?: string;
  managed?: string;
  host?: string;
  ingress?: IngressSpec;
  desired?: { image?: string; compose?: ComposeBlock };
}

export interface IngressRule {
  path: string;
  app: string;
  port: number;
}

export interface IngressSpec {
  host: string;
  aliases?: string[];
  ssl_redirect?: boolean;
  basic_auth?: boolean;
  tls?: { enabled?: boolean; min_version?: string; cert_path?: string };
  rules?: IngressRule[];
}

export interface DatabaseSpec {
  engine: string;
  version: string;
  image: string;
  database: string;
  user: string;
  hasPassword: boolean;
  host: string;
  port: number;
  volume: string;
  dsn: string;
}

function summaryOf(app: ResourceSnapshot): VMSummary {
  return app.summary_json as unknown as VMSummary;
}

function composeImage(s: VMSummary): string {
  return s.desired?.compose?.image ?? s.image ?? "";
}

/** True for VM/compose resources; k8s apps are never reclassified. */
export function isComposeResource(app: ResourceSnapshot): boolean {
  return summaryOf(app).runtime === "compose";
}

export function classifyVMResource(app: ResourceSnapshot): VMResourceType {
  const s = summaryOf(app);
  if (s.runtime !== "compose") return "app";
  if (s.managed === "ingress") return "ingress";
  if (s.managed && DB_ENGINES.has(s.managed)) return "database";
  const img = composeImage(s);
  if (PROXY_IMAGE_RE.test(img)) return "ingress";
  if (DB_IMAGE_RE.test(img)) return "database";
  return "app";
}

/** Structured routing/TLS spec, when the backend persisted one. */
export function extractIngressSpec(app: ResourceSnapshot): IngressSpec | null {
  const s = summaryOf(app);
  if (s.ingress && s.ingress.host) return s.ingress;
  if (s.host) return { host: s.host, rules: [] };
  return null;
}

function imageBase(image: string): string {
  let repo = image.split("@")[0];
  const slash = repo.lastIndexOf("/");
  if (slash >= 0) repo = repo.slice(slash + 1);
  return repo.split(":")[0].toLowerCase();
}

function imageVersion(image: string): string {
  const noDigest = image.split("@")[0];
  const colon = noDigest.lastIndexOf(":");
  if (colon < 0) return "";
  const tag = noDigest.slice(colon + 1);
  return tag.split("-")[0];
}

function firstContainerPort(ports: string[] | undefined, fallback: number): number {
  for (const p of ports ?? []) {
    const spec = p.split("/")[0];
    const parts = spec.split(":");
    const container = parts.length === 2 ? parts[1] : parts[0];
    const n = parseInt(container, 10);
    if (!Number.isNaN(n)) return n;
  }
  return fallback;
}

function namedVolume(volumes: string[] | undefined): string {
  for (const v of volumes ?? []) {
    const parts = v.split(":");
    if (parts.length >= 2 && parts[0] && !parts[0].startsWith("/") && !parts[0].startsWith(".")) {
      return parts[0];
    }
  }
  return "";
}

/**
 * ServiceDatabase fields, parsed from the persisted spec (managed) or the
 * adopted compose block (image → engine/version, POSTGRES_ or MYSQL_ env, the
 * named data volume, the published port). The DSN masks the password.
 */
export function extractDatabaseSpec(app: ResourceSnapshot): DatabaseSpec {
  const s = summaryOf(app);
  const compose = s.desired?.compose ?? {};
  const image = compose.image ?? s.image ?? "";
  const env = compose.environment ?? {};

  let engine = s.managed && DB_ENGINES.has(s.managed) ? s.managed : imageBase(image);
  if (engine === "postgresql") engine = "postgres";

  const isPg = engine === "postgres";
  const isMy = engine === "mysql" || engine === "mariadb";

  const database = env.POSTGRES_DB ?? env.MYSQL_DATABASE ?? env.MONGO_INITDB_DATABASE ?? "";
  const user =
    env.POSTGRES_USER ?? env.MYSQL_USER ?? env.MONGO_INITDB_ROOT_USERNAME ?? (isPg ? "postgres" : "");
  const hasPassword = Boolean(
    env.POSTGRES_PASSWORD ?? env.MYSQL_PASSWORD ?? env.MYSQL_ROOT_PASSWORD ?? env.MONGO_INITDB_ROOT_PASSWORD,
  );

  const defaultPort = isPg ? 5432 : isMy ? 3306 : engine === "redis" ? 6379 : engine === "mongo" ? 27017 : 0;
  const port = firstContainerPort(compose.ports, defaultPort);
  const host = app.name;
  const scheme = isPg ? "postgresql" : isMy ? "mysql" : engine;

  let dsn = "";
  if (scheme && host) {
    const auth = user ? `${user}${hasPassword ? ":••••••" : ""}@` : "";
    dsn = `${scheme}://${auth}${host}:${port}${database ? "/" + database : ""}`;
  }

  return {
    engine,
    version: imageVersion(image),
    image,
    database,
    user,
    hasPassword,
    host,
    port,
    volume: namedVolume(compose.volumes),
    dsn,
  };
}
