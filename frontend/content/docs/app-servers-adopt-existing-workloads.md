# Adopt existing workloads on a VM

## What it's for

You've got containers already running on a connected VM (your own compose stack, or something
migrated over) — pull them into Dada as first-class managed applications, each with its own
logs, metrics, and settings, without rebuilding or restarting from scratch.

## How to adopt

1. Open the **App Server** (it must be **Ready** — discovery runs through the Portainer edge
   agent, so it can't work before enrollment finishes).
2. Go to the **Workloads** tab.
3. Click **Discover workload** (or **Re-scan** to refresh). This is **read-only** — nothing
   changes on the VM until you explicitly import. You'll see each running container's image,
   ports, and volumes; named volumes are flagged with an "N vol" badge meaning they'll be kept
   as external volumes (data-safe).
4. Click **Import into Dada**. In the import wizard:
   - **Application name** for the group.
   - A per-container checklist — toggle **Include** on each service you want, and edit its
     **service name** if you want something different from the auto-slugified default.
   - Optionally paste a **.env** file. If you do, you must check the consent box acknowledging
     these values will be committed to git **in plaintext** — don't paste real secrets here.
   - Optionally preview the generated `compose.yaml` before submitting.
5. Click **Import & deploy**. The console polls the operation until it finishes, then redirects
   you to **Applications**.

Each service you selected is now its **own** Dada application — not one combined app.

## Gotchas

- Discovery is **read-only, via Portainer, no SSH** — it can't see anything until the server
  is enrolled and **Ready**. If you land here on a `WaitingForAgent` server, you'll get a "not
  enrolled yet" placeholder, not an error — it's just not ready, not your fault.
- **.env values are committed to git in plaintext** if you paste them — the consent checkbox
  is mandatory precisely because there is no secret-encryption step in this path. For real
  secrets, add them afterward through the app's Environment Variables tab (which supports a
  `secret` flag) instead of pasting them in the import wizard.
- Named volumes are preserved as `external` in the generated compose so data isn't lost, but
  double-check the discovery preview's warnings block before importing if anything looks off.
- Import can time out on very large stacks; if it does, re-scan and retry rather than assuming
  it failed silently.

## Not yet supported

- Selective re-import / adding a newly-appeared container to an already-imported app (you
  re-run discovery and import fresh, per-container).
- Encrypting `.env` values during import — that only happens later, per-variable, in app
  settings.
