export type DomainEndpointFailure = "already_exists" | "not_verified" | "other";

export interface DomainEndpointErrorLike {
  status?: number;
  code?: string;
}

/**
 * Classifies a failed CreateEndpoint call the app-detail page's "Add Domain"
 * modal makes (endpointsApi.create, page.tsx's handleDomainCreate).
 *
 * This exists because of a live incident: drariaatashin@gmail.com used that
 * modal for their own domain before verifying it, hit HTTP 409 three times
 * in 40 seconds, and abandoned the product -- the last thing they saw was
 * the raw backend sentence "a domain with that FQDN already exists in this
 * environment" rendered verbatim into the Russian UI, with no indication of
 * what to do next. The endpoint modal creates a PublicApi record; it does
 * NOT create the Ingress that makes a custom domain actually serve traffic.
 * Only the Domains page funnel ("Добавить хост" -> "Направить на
 * приложение", customDomainsApi.attachHostname) writes the domain_hostnames
 * row that produces an Ingress. So both failure modes this function
 * recognizes point the user at the Domains page rather than at a retry of
 * the same dead-end modal:
 *
 * - 409 / code "fqdn_taken": the FQDN is already registered as an endpoint
 *   in this environment. Retrying the same form cannot succeed.
 * - 403 / code "no_verified_apex": the backend's ownership gate rejected
 *   the FQDN because its apex domain has no VERIFIED domain authorization
 *   for this project yet. This is what stops someone from creating a dead
 *   endpoint for a domain they have not proven they own.
 *
 * Reads status/code defensively because apiFetch attaches them to the
 * thrown Error as optional extra properties, not as part of the Error
 * type itself.
 */
export function classifyDomainEndpointError(err: unknown): DomainEndpointFailure | null {
  if (!(err instanceof Error)) return null;
  const e = err as Error & DomainEndpointErrorLike;
  if (e.status === 409 || e.code === "fqdn_taken") return "already_exists";
  if (e.status === 403 || e.code === "no_verified_apex") return "not_verified";
  return "other";
}
