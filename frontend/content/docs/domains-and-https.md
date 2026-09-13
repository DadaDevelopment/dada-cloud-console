# Custom domains and HTTPS

## What it's for

Serve your app on your own domain instead of the platform's default, with a TLS certificate
issued and renewed automatically.

This is a two-step model:

1. **Authorize an apex domain** you own (project-level, on the **Domains** page).
2. **Attach a hostname** under that apex to a specific app (per-app, in that app's
   **Settings → Domains** tab).

## Step 1 — Authorize your apex domain

1. Go to **Domains** (project-level nav) → **Add Domain**.
2. Enter the **apex (root) domain** you own, e.g. `acme.com` — no `http://`, no subdomain, no
   path.
3. You'll get a **TXT record** challenge: a **Type**, **Host/Name**, and **Value** to add at
   your DNS provider. Each has a copy button.
4. Add the record at your DNS provider, then come back and click **Verify**.
5. Verification is **manual** — there's no auto-polling. If your DNS hasn't propagated yet,
   just click Verify again in a few minutes.
6. Once verified, the apex **and all its subdomains** are authorized for this project.

## Step 2 — Attach a hostname to an app

1. Open the app you want to expose → **Settings** → **Domains** tab.
2. Enter the hostname, e.g. `shop.acme.com` (must be the apex itself or a subdomain of an
   already-verified apex for this project).
3. Click **Attach**. You'll get a DNS hint (record type/host/target) to point at the platform
   ingress if you haven't already.
4. The TLS certificate is issued automatically once DNS resolves to the platform.
5. The hostname appears in a table with **Status** and **Certificate** columns so you can see
   when it's live.

## Gotchas

- **Attach happens on the app, not on the Domains page.** The Domains page only handles
  apex-level ownership verification — you won't find a "attach subdomain" control there. This
  matches the inventory's "split-brain" observation; it hasn't been unified yet.
- **Verify is manual**, no auto-poll — expect to click it once, wait for DNS propagation ("can
  take a few minutes"), and click it again.
- **Removing a domain authorization requires detaching every hostname under it first** — the
  confirm dialog says so explicitly. Detach hostnames in each app's Settings → Domains tab
  before trying to remove the apex.
- Detaching a hostname removes its TLS certificate and ingress immediately (the app itself
  keeps running on the platform's default URL).

## Networking: visitor IPs and outbound addresses

What reaches your app and what leaves it matters if you run the app as a reverse proxy in
front of your own origin server, or build any address-based protection.

**Inbound requests.** The platform ingress sets the usual `X-Forwarded-For`, `X-Real-IP`,
`X-Forwarded-Proto` and `X-Forwarded-Host` headers. However, **the visitor's real IP is not
delivered today**: the public load balancer in front of the cluster operates at L4 and rewrites
the source address, so `X-Forwarded-For` and `X-Real-IP` carry the balancer's own address
(`155.212.223.198`), not the visitor's. Do not build rate limits, geo logic or anti-fraud on
these headers — every visitor looks the same.

**Outbound requests** (your app → your origin or any external service) leave from the public
addresses of the cluster nodes. The node set changes with scaling and maintenance and there is
**no fixed list of ranges** — an IP allowlist on the origin will break on the first node
replacement. Protect the origin with something that cannot be spoofed instead:

- a secret header (e.g. `X-Origin-Token`) your app adds to every request and the origin
  verifies; keep the value in the app's environment variables;
- mTLS: a client certificate on the app side, verified by the origin;
- an origin that is closed to the internet entirely, reached over a tunnel/VPN from the app.

## Not yet supported

- The visitor's real IP in `X-Forwarded-For` / `X-Real-IP` — needs PROXY protocol between
  the load balancer and the ingress on the hosting provider's side.
- Static egress IPs.
- Auto-polling verification (you must click Verify yourself).
- Attaching/managing hostnames from the Domains page itself — it's app-settings-only today.
