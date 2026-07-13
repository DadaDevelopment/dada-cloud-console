# Managed DNS via NS delegation + wizard dual-path

Status: design (brainstorming locked 2026-07-13)
Owner: platform
Related: [[project_custom_domain_dead_target]], docs/plans/2026-05-11-v2-domain-management.md

## Understanding summary

- Today custom domains use one path only: the console tells the user an A/CNAME
  target (the ingress LB) and issues the cert with cert-manager HTTP-01. The
  wizard hard-codes that single "point your DNS at this IP" flow.
- We want a second, self-service path: the user delegates their whole domain to
  our nameservers (NS records -> ns1/ns2.dada-tuda.ru) and we manage the zone
  for them (apex/www routing, cert, and a full record editor).
- The wizard should expose both paths as a toggle, like a code-snippet language
  switcher: "Point a record (advanced)" vs "Delegate to us (we do the rest)".
- Grounded infra facts (live, 2026-07-13):
  - `45.90.32.31` is a cluster node's public IP (internal 10.16.0.10) and it
    answers on :53 (powerdns). `ns1.dada-tuda.ru` and `ns2.dada-tuda.ru` already
    A-resolve to it. powerdns currently serves zero zones.
  - `dada-tuda.ru` itself is hosted at Beget, not on our powerdns. So delegating
    a *customer* domain to `ns1/ns2.dada-tuda.ru` needs no registrar glue (out of
    bailiwick); we only need the ns1/ns2 A records at Beget (already present).
  - `cert-manager-webhook-powerdns` is installed -> DNS-01 through powerdns is
    infra-ready.
  - powerdns API: ClusterIP `powerdns-api:8081`, auth via `API_KEY` env in the
    powerdns pod. Backend can reach it in-cluster.
  - Public LB provisioning is manual/bare-metal (most LB services are
    `<pending>`); only ingress-nginx-pub has an IP. So a 2nd NS IP is a manual
    node-IP assignment, deferred.

## Non-goals (MVP)

- Second redundant NS on a distinct IP (deferred; single node accepted).
- DNSSEC.
- Registrar API automation (user still changes NS at their registrar by hand).
- Full AXFR import (blocked by source NS); import is a best-effort probe of a
  fixed record-name set that the user confirms/edits before cutover.
- Secondary/hidden-master, zone transfer to external NS.

## Assumptions

- Single authoritative node (45.90.32.31) is acceptable availability for MVP.
- Users delegating to us accept that we become authoritative for the whole zone.
- powerdns API is reachable and writable from the backend with the pod API key
  (to be surfaced as a backend secret). [verify in slice 1]
- A ClusterIssuer using the powerdns DNS-01 webhook solver can be created and
  issue for arbitrary customer apexes. [verify in slice 1]

## Decision log

| # | Decision | Alternatives | Why |
|---|----------|--------------|-----|
| 1 | Keep A/CNAME path AND add NS path; wizard toggle | replace old path | old path is correct for users who won't delegate; both coexist |
| 2 | MVP single NS node (45.90.32.31) | 2 IPs now | infra already answers there; 2nd IP is manual, low value for first users |
| 3 | Auto-import current zone before cutover | blank zone | blank zone kills the user's existing site/email (ggrk52 pansionat on .68 + possible MX) |
| 4 | DNS-01 via powerdns for delegated domains | HTTP-01 | issues before the site resolves to us, supports wildcard, no 404-challenge fragility |
| 5 | Full DNS editor in console over powerdns API | route-only | user asked for real managed-DNS; powerdns API makes CRUD cheap |
| 6 | Import = probe fixed record names, user confirms | AXFR | AXFR blocked at source NS; probe of apex/www/mail/MX/TXT covers common cases |

## Architecture

Components:
- powerdns (authoritative, :53 on node 45.90.32.31) -- existing, empty.
- `powerdns-api:8081` -- zone/record CRUD backend, key `API_KEY`.
- Backend: new `internal/pdns` client + `managed_dns` model/migration + handlers
  under the existing domains API. Delegation detection poller. DNS-01 wiring.
- gitops-agent: per-domain Certificate switched to DNS-01 issuer for delegated
  domains (A-record domains keep HTTP-01).
- Frontend: wizard toggle + record editor UI.

Data model (new): a domain (already `domain_authorizations`) gains a
`delegation_mode` (`record` | `delegated`) and a `managed_zone` row per delegated
apex tracking powerdns zone id + status (`awaiting_ns` -> `active`). Records are
read/written live through powerdns API (source of truth = powerdns), not mirrored
in our DB, to avoid drift.

## Flow (delegated path)

1. User picks "Delegate to us" for apex `example.com`.
2. Backend probes the current authoritative NS for a fixed name set (apex A/AAAA,
   www, MX, common TXT like SPF/DMARC) and shows the found records for
   confirm/edit. This is the import preview.
3. On confirm: backend creates the powerdns zone, writes the confirmed records +
   apex/www A -> ingress LB (155.212.223.198), and shows the user
   `ns1.dada-tuda.ru` / `ns2.dada-tuda.ru` to set at their registrar.
4. Delegation poller: `dig NS example.com` until our NS appear (propagation).
5. On detected delegation: mark zone `active`, request a DNS-01 cert for the
   apex (+ www), attach the app Ingress. Reconciler flips hostname active.
6. Record editor: full CRUD via powerdns API for anything else the user wants.

## Risks

- Single NS = SPOF; a node reboot drops all delegated domains' DNS. Accept for
  MVP, prioritize 2nd NS before onboarding many domains.
- Import is incomplete by nature (no AXFR); a record we don't probe for is lost
  on cutover. Mitigate: show "we found these; add anything missing before you
  switch NS" and keep old NS working until user confirms.
- powerdns is now customer-facing on :53 -- harden (rate-limit, no recursion,
  no AXFR to the world, API key only in-cluster).
- Making powerdns authoritative for a domain whose NS is NOT yet delegated is
  harmless (nobody queries us yet), so provisioning-before-delegation is safe.

## Slices

- S1 backend infra: `internal/pdns` client (verify API key + issuer), migration
  (`delegation_mode`, `managed_zones`), zone create/list/record CRUD handlers,
  delegation poller. Ships behind a feature flag, no UI yet.
- S2 cert (OPTIONAL, deferred): DNS-01 via the existing letsencrypt-dns01
  issuer. NOT a blocker -- because delegate seeds apex/www -> our LB in the zone,
  once NS propagates the domain resolves to us and the existing HTTP-01 attach
  path issues the cert normally. DNS-01 only adds wildcard + issue-before-propagation.
- S3 import: probe-based zone import preview endpoint.
- S4 frontend: wizard dual-path toggle + record editor.
- S5 hardening: powerdns recursion/AXFR/rate-limit lockdown; runbook; 2nd NS
  follow-up ticket.
