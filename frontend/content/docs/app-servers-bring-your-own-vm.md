# Bring your own VM (App Servers)

## What it's for

You already have a VPS or bare-metal server. Instead of migrating off it, connect it to Dada
over SSH once — we install Docker and an edge agent, and from then on you manage it (and
deploy to it) from the console like any other resource. No app has to move.

## How to connect

1. Go to **App Servers** → **Add VM**.
2. Give it a **Name** (lowercase, DNS-style — used for the VM record and its Portainer
   endpoint).
3. Under **Source**, choose **Connect existing VM**.
4. Fill in:
   - **VM IP address**
   - **SSH port** (default `22`)
   - **SSH user** (default `root`)
   - **SSH private key** — pasted directly. It is used **once** to install Docker and the edge
     agent, then discarded; it is never stored.
5. Submit. Status starts at **Provisioning**, then **WaitingForAgent** (blue → amber) while the
   edge agent phones home, then **Ready** (green) once Portainer confirms the heartbeat.

## Gotchas

- **The SSH key is never stored** — if the connect attempt fails partway, you'll need to paste
  it again to retry (see below). There's no "save for later."
- **WaitingForAgent has no built-in timeout/progress indicator** in the current UI — if it sits
  there a long time, check that the VM can reach the platform's network and that Docker
  installed correctly; the console won't tell you what's wrong on its own yet.
- **If the connect fails** (status **Failed**), use **Retry connect** on the server's detail
  page to paste the SSH key again. The **VM IP is read-only in the retry modal** — you cannot
  fix a wrong IP this way. If the IP itself was wrong, delete the server and create it again
  with the correct IP.
- **Delete** is available from the App Servers list (row action) for anyone with manage
  permission — deleting redirects you to Operations to watch it finish.

## After it's Ready

Already have containers running on it? Head to
[Adopt existing workloads](app-servers-adopt-existing-workloads.md) to bring them under
management without rebuilding anything.

## Not yet supported

- Changing the VM IP on an existing server (delete + recreate is the only path).
- A visible timeout or actionable diagnostics while stuck in `WaitingForAgent`.
- Deploying a brand-new app to a VM environment from the Applications page — VM environments
  are deliberately blocked there; use App Servers workflows instead.
