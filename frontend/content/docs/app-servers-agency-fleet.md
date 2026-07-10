# Running a fleet of client VMs (agency playbook)

> **Doc-vs-code note:** there is no separate "fleet" feature in the UI — this guide describes
> how to use the existing **App Servers** list to manage many client VMs under one stack. If
> you were expecting a dedicated fleet dashboard, it doesn't exist yet; everything below is the
> same App Servers page described in
> [Bring your own VM](app-servers-bring-your-own-vm.md), used at scale.

## What it's for

If you run an agency and host multiple clients' VMs, you don't need a separate tool per
client. Connect (or order) each client's VM as its own App Server inside one project, and get
unified monitoring/logs/metrics across all of them from the same console.

## How to set it up

1. For each client VM: go to **App Servers** → **Add VM**, and either
   [connect their existing server](app-servers-bring-your-own-vm.md) or
   [order a new one](app-servers-order-a-vm.md) on their behalf.
2. Repeat for every VM you manage. They all show up in one **App Servers** list/table with
   **Name**, **Status**, **VM IP**, **Portainer** link, and heartbeat.
3. For any VM with an existing compose stack, use
   [Adopt existing workloads](app-servers-adopt-existing-workloads.md) to bring each client's
   services in as individually monitored applications.
4. Set up [Monitoring](monitoring-metrics-logs-alerts.md) per app so you have per-client alerts
   and dashboards you can show as a reliability guarantee.

## Gotchas

- There's no per-client grouping, tagging, or sub-project boundary in the App Servers list
  today — naming convention (e.g. prefixing server names with the client name) is your only
  organizational tool.
- All servers in a project share the same **Members/Roles** access — you can't currently give
  one team member access to only one client's VM within the same project. Use separate
  projects per client if you need hard isolation (see
  [Members and roles](members-and-roles.md)).
- Deleting a VM is a row action on the list — double check you're deleting the right client's
  server; there's no per-VM confirmation beyond the standard delete confirm.

## Not yet supported

- A dedicated multi-tenant "fleet" view, client tagging, or per-VM access scoping within a
  single project.
- Bulk actions (e.g. re-scan/discover across all VMs at once).
