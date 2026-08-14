/**
 * Client-side mirror of the backend's connection-string detector
 * (`backend/internal/api/envvars.go: connectionStringWarning`).
 *
 * Exists because of a live incident: a user copied the bare `host` field off
 * the database page (`pg-router.databases.svc.cluster.local`) into
 * `DATABASE_URL`, and the console accepted it silently, eight times in a
 * row, while the app sat in a crash loop for twelve hours. The value is
 * never rejected -- a user may store any string they want -- but this lets
 * the form warn as the user types, before they even submit, instead of only
 * after a round trip to the server.
 *
 * `HAS_SCHEME_PREFIX` requires the literal scheme separator: a bare
 * `host:5432` must not be mistaken for a scheme the way a permissive URL
 * parser would half-read it (treating "host" as the scheme and "5432" as
 * opaque data).
 */

const CONNECTION_KEY_SUFFIXES = ["_URL", "_DSN", "_CONNECTION_STRING"];

const CONNECTION_KEY_EXACT = new Set([
  "DATABASE_URL",
  "DATABASE_DSN",
  "REDIS_URL",
  "MONGO_URL",
  "MONGODB_URL",
  "POSTGRES_URL",
  "POSTGRESQL_URL",
  "MYSQL_URL",
  "AMQP_URL",
  "RABBITMQ_URL",
  "CONNECTION_STRING",
]);

const HAS_SCHEME_PREFIX = new RegExp("^[a-zA-Z][a-zA-Z0-9+.-]*:" + "/" + "/");

/**
 * Reports whether `key` conventionally holds a full connection string
 * (a DSN or URL) rather than a bare value.
 */
export function looksLikeConnectionKey(key: string): boolean {
  const upper = key.toUpperCase();
  if (CONNECTION_KEY_EXACT.has(upper)) return true;
  return CONNECTION_KEY_SUFFIXES.some((suffix) => upper.endsWith(suffix));
}

/**
 * Reports whether `value` is non-empty, `key` looks like a connection string
 * field, and `value` has no scheme prefix -- i.e. it is almost certainly a
 * bare host, a host:port pair, or some other fragment rather than the full
 * string the runtime needs.
 */
export function isBareConnectionValue(key: string, value: string): boolean {
  if (!value || !looksLikeConnectionKey(key)) return false;
  return !HAS_SCHEME_PREFIX.test(value);
}
