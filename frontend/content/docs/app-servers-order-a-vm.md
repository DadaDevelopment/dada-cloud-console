# Order a managed VM (App Servers)

## What it's for

You don't have a server yet and don't want to shop for one — order a VM straight from the
console and we provision and bootstrap it for you (Docker + edge agent included).

## How to order

1. Go to **App Servers** → **Add VM**.
2. Give it a **Name** (lowercase, DNS-style).
3. Under **Source**, choose **Provision (Terraform)**.
4. Fill in:
   - **Flavor** — small / medium / large.
   - **Region** — `ru1` / `ru2` / `kz1` / `eu1`.
   - **OS image** — free-text, defaults to `ubuntu-22.04`.
   - **SSH key name** — free-text, defaults to `dada-agent`.
5. Submit. Status goes **Provisioning** → **WaitingForAgent** → **Ready**, same as a
   bring-your-own VM.

## Gotchas

- This path skips the SSH-key-pasting step entirely — provisioning and agent install are
  handled for you.
- Same status caveats as bring-your-own: **WaitingForAgent** has no built-in timeout in the
  current UI.
- Once ordered, you manage the server the same way regardless of how it was created (list
  shows both terraform- and manually-connected VMs side by side, with **Delete** available as
  a row action for anyone with manage permission).

## After it's Ready

Deploy to it by connecting to the VM directly (SSH in yourself) and setting up your compose
stack, then use [Adopt existing workloads](app-servers-adopt-existing-workloads.md) to bring
those containers into Dada as managed applications — there is no "create a fresh Compose app"
form in the console; adoption is the only way a compose-based app enters Dada today.

## Not yet supported

- Choosing a custom OS image/SSH key from a picker (both fields are free-text, no validation
  against what images/keys actually exist for the region).
- A guided "deploy your first compose stack to this new VM" flow — you still SSH in yourself
  and then adopt.
