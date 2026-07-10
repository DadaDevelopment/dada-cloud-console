# Monitoring: metrics, logs, and alerts

## What it's for

Know whether your service is alive, see where a problem is, and get alerted before your users
notice — for your own team, or as a reliability guarantee you can show clients.

## How to set it up

1. Go to **Monitoring** → **Create Monitoring App**, give it a name.
2. You'll immediately see an onboarding card with 3 steps:
   - **Step 1 (done automatically):** the monitoring app is created.
   - **Step 2:** a one-shot **API key** is shown once — copy it now (there is no way to view it
     again later; losing it means creating a new key via the app's own page).
   - **Step 3:** instrumentation snippets in **Node.js**, **Python**, **Env vars**, or **curl**
     tabs — each pre-filled with your API key and the ingest endpoint. `service.instance.id` is
     optional and becomes a filterable "source" label.
3. The card's status updates automatically: **Checking…** → **Waiting for first telemetry** →
   **Receiving** (polled every few seconds, no refresh needed).
4. Once data arrives, open the app for **Overview** (health, last-seen, 15-minute error rate,
   firing alerts), **Metrics**, **Logs**, and **Alerts** tabs.

## Alerts and notifications

1. On an app's **Alerts** tab, add an **Alert Rule**: pick the metric, condition, threshold,
   duration, and a notification channel (or "— none —" to just record the firing state).
2. Configure **Notification Channels** separately:
   - **Telegram** — bot token + chat ID.
   - **Email** — comma-separated addresses.
   - **Webhook** — a URL.
3. Use **Open in Grafana** on the app for a deeper, ad-hoc view of the same data.

## Gotchas

- The API key is shown **exactly once**, at creation time. If you lose it, you can't recover
  it from the console — you'll need to generate a new one from the app's page (check its
  "manage keys" link) and re-instrument.
- Monitoring is opt-in and manual today: deploying an app via GitHub/image does **not**
  automatically create a monitoring app for it. You have to create one yourself and wire the
  OTLP SDK/env vars in.
- The curl test snippet uses the current clock second as the counter value specifically so the
  console renders a real rate instead of a flat line — re-run it a few times if you want to see
  a moving chart.

## Not yet supported

- Auto-attaching monitoring to a newly deployed app (still a manual "Create Monitoring App"
  step, separate from deploy).
- Custom dashboards in the UI (the capability exists in the backend but is not exposed here —
  use **Open in Grafana** for anything beyond the built-in Overview/Metrics/Logs/Alerts tabs).
