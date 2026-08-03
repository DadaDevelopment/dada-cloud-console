#!/usr/bin/env python3
"""Recompute Dada Cloud unit economics from the LIVE cluster, not from a spreadsheet.

WHY THIS IS A SCRIPT AND NOT A TABLE IN A DOC. Every number in
docs/product/unit-economics.md is a ratio between two things that both move on
their own: the Beget invoice (moves when a node is added) and the share of the
cluster the platform eats before anything is sellable (moves every time a
platform component is scaled). A table typed by hand is correct on the day it is
typed and quietly wrong a week later, and the direction of the error is always
flattering — capacity grows in the doc, the invoice grows in reality.

THE ONE INPUT THIS CANNOT DISCOVER. The invoice. Beget does not expose it to the
cluster, so BILL_RUB is passed in and is the only figure here that a human has to
keep honest. Everything else is read from kubectl.

WHY THE COST WEIGHTS ARE RAM-HEAVY AND NOT CPU-HEAVY, unlike
config/billing/cluster-cost.yaml: the weights answer "which dimension runs out
first", and on the live cluster that is memory and disk, not cores. Weighting
CPU most heavily charges the bill against the dimension nobody is competing for,
and understates the price of the one that actually caps how many tenants fit.

TWO COST FIGURES, and the second is the one to price against:
  marginal — invoice divided by the WHOLE cluster. What one more GiB costs when
             there is room. Correct for "should we take this tenant".
  loaded   — invoice divided by the SELLABLE remainder, i.e. after the platform's
             own consumption is carved out. This is the price floor: at 100%
             occupancy of what is actually for sale, this rate recovers the whole
             invoice and not a ruble more.

Usage:
    BILL_RUB=15000 scripts/unit-economics.py
    BILL_RUB=15000 MGMT_CONTEXT=e7b608-client-super-admin@e7b608-client \\
        scripts/unit-economics.py

    DATABASE_URL=postgres://...  adds the revenue arm (payments, plans, box ledger).
    Without it the revenue section is SKIPPED LOUDLY rather than printed as zero —
    a missing tunnel and an empty payments table must not look the same.

Requires: kubectl with a working context. Sandboxed shells block the k8s API;
run outside the sandbox.
"""

import json
import os
import subprocess
import sys

WEIGHTS = (0.25, 0.50, 0.25)

PRICE_VCPU_MONTH = 350.0
PRICE_RAM_GIB_MONTH = 450.0
PRICE_DISK_GIB_MONTH = 30.0
PRICE_MIN_INVOICE = 199.0

AMVERA = [
    ("Пробный", 170, 0.1, 0.1, 2),
    ("Начальный", 290, 0.25, 0.5, 5),
    ("Начальный+", 490, 0.5, 1.0, 7),
    ("Стандартный", 1450, 1.0, 2.5, 15),
    ("Ультра", 4300, 2.0, 6.0, 25),
]

BOX_NAMESPACE = "dada-boxes"

TENANT_LABEL = "dada.io/project"

OWN_PROJECTS = {"internal", "platform", "client-a", "fin-core"}
"""Projects owned by the operator rather than by a signup.

These are real workloads and they consume real capacity, but they will never
send an invoice back. Lumping them in with customer namespaces is the single
easiest way to make coverage look twice as good as it is, so they are named
here explicitly and counted apart. A project that starts as ours and is later
sold to somebody must be removed from this set by hand — there is no signal in
the cluster that would do it automatically."""


def kubectl(args, context=None):
    """Run kubectl and parse JSON, returning None when the call fails.

    Fails soft on purpose: the mgmt cluster is optional, and a missing second
    context must degrade the capacity figure visibly rather than abort the run.
    """
    cmd = ["kubectl"]
    if context:
        cmd += ["--context", context]
    cmd += args + ["-o", "json"]
    for _ in range(4):
        try:
            out = subprocess.run(cmd, capture_output=True, timeout=90)
        except subprocess.TimeoutExpired:
            continue
        if out.returncode == 0:
            return json.loads(out.stdout)
    sys.stderr.write("WARN: kubectl failed: %s\n" % " ".join(cmd))
    return None


def parse_cpu(value):
    """Convert a k8s cpu quantity to whole vCPU."""
    if not value:
        return 0.0
    return float(value[:-1]) / 1000 if value.endswith("m") else float(value)


def parse_mem(value):
    """Convert a k8s memory quantity to GiB."""
    if not value:
        return 0.0
    for suffix, factor in (("Ki", 1 / 2 ** 20), ("Mi", 1 / 1024), ("Gi", 1.0),
                           ("Ti", 1024.0)):
        if value.endswith(suffix):
            return float(value[: -len(suffix)]) * factor
    return float(value) / 2 ** 30


def bucket_of(namespace, projects):
    """Classify a namespace as the box pool, an own project, a tenant, or platform.

    The discriminator is the dada.io/project label the platform stamps on every
    namespace it provisions. Anything without that label was created by us for
    the platform to run at all, and is overhead by definition — no namelist to
    maintain and no way for a new infra namespace to be silently counted as
    revenue-bearing.
    """
    if namespace == BOX_NAMESPACE:
        return "BOX"
    project = projects.get(namespace)
    if project is None:
        return "PLATFORM"
    return "OWN" if project in OWN_PROJECTS else "TENANT"


def collect(context=None):
    """Read allocatable capacity and per-namespace requests from one cluster."""
    nodes = kubectl(["get", "nodes"], context) or {"items": []}
    namespaces = kubectl(["get", "ns"], context) or {"items": []}
    pods = kubectl(["get", "pods", "-A"], context) or {"items": []}
    pvcs = kubectl(["get", "pvc", "-A"], context) or {"items": []}
    lh = kubectl(["get", "nodes.longhorn.io", "-n", "longhorn-system"], context)

    cap_cpu = sum(parse_cpu(n["status"].get("allocatable", {}).get("cpu"))
                  for n in nodes["items"])
    cap_ram = sum(parse_mem(n["status"].get("allocatable", {}).get("memory"))
                  for n in nodes["items"])

    cap_disk = 0.0
    if lh:
        for node in lh["items"]:
            for disk in (node.get("status", {}).get("diskStatus") or {}).values():
                cap_disk += disk.get("storageMaximum", 0) / 2 ** 30

    used = {}
    for pod in pods["items"]:
        if pod["status"].get("phase") not in ("Running", "Pending"):
            continue
        ns = pod["metadata"]["namespace"]
        entry = used.setdefault(ns, [0.0, 0.0, 0.0])
        for container in pod["spec"]["containers"]:
            req = container.get("resources", {}).get("requests", {})
            entry[0] += parse_cpu(req.get("cpu"))
            entry[1] += parse_mem(req.get("memory"))
    for pvc in pvcs["items"]:
        ns = pvc["metadata"]["namespace"]
        entry = used.setdefault(ns, [0.0, 0.0, 0.0])
        size = pvc["spec"].get("resources", {}).get("requests", {}).get("storage", "0")
        entry[2] += parse_mem(size)

    projects = {}
    for ns in namespaces["items"]:
        label = (ns["metadata"].get("labels") or {}).get(TENANT_LABEL)
        if label:
            projects[ns["metadata"]["name"]] = label

    return {"cap": [cap_cpu, cap_ram, cap_disk], "ns": used, "projects": projects}


def amvera_tier(vcpu, ram):
    """Map a footprint onto the closest Amvera tier by COMPUTE only.

    Deliberately ignores disk. An earlier version matched disk too and pushed a
    1 GiB tenant onto the 6 GiB "Ультра" tier purely to reach its bundled 25 GB
    of storage, producing a 4300 rub price for a footprint worth a tenth of that.
    Retail PaaS sells extra disk as an add-on, so disk must not select the tier.
    """
    for name, price, tier_cpu, tier_ram, _ in AMVERA:
        if vcpu <= tier_cpu and ram <= tier_ram:
            return name, float(price)
    return "выше Ультра", float(AMVERA[-1][1])


def our_price(vcpu, ram, disk):
    """Metered price under the proposed rate card, with the minimum invoice applied."""
    metered = (vcpu * PRICE_VCPU_MONTH + ram * PRICE_RAM_GIB_MONTH
               + disk * PRICE_DISK_GIB_MONTH)
    return max(metered, PRICE_MIN_INVOICE), metered


def revenue_arm(database_url):
    """Print realised revenue and box-ledger consumption, or say why it was skipped."""
    if not database_url:
        print("\nВЫРУЧКА: ПРОПУЩЕНА — DATABASE_URL не задан. Это не ноль, это «не измеряли».")
        return
    queries = [
        ("Планы", "select plan, count(*) from billing_accounts group by 1 order by 2 desc"),
        ("Успешные платежи",
         "select coalesce(sum(amount_value),0), count(*) from payments where status='succeeded'"),
        ("Бокс-лежер",
         "select kind, count(*), round(sum(cost_rub),2) from box_usage group by 1"),
    ]
    print("\nВЫРУЧКА (факт из БД):")
    for title, sql in queries:
        out = subprocess.run(["psql", database_url, "-At", "-F", " | ", "-c", sql],
                             capture_output=True, text=True)
        if out.returncode != 0:
            print("  %s: ОШИБКА %s" % (title, out.stderr.strip().splitlines()[:1]))
            continue
        print("  %s: %s" % (title, out.stdout.strip().replace("\n", " ; ") or "нет строк"))


def main():
    bill = float(os.environ.get("BILL_RUB", "15000"))
    mgmt = os.environ.get("MGMT_CONTEXT")

    prod = collect()
    cap = list(prod["cap"])
    namespaces = dict(prod["ns"])
    projects = dict(prod["projects"])
    if mgmt:
        extra = collect(mgmt)
        for i in range(3):
            cap[i] += extra["cap"][i]
        for ns, vals in extra["ns"].items():
            entry = namespaces.setdefault("mgmt/" + ns, [0.0, 0.0, 0.0])
            for i in range(3):
                entry[i] += vals[i]
        for ns, project in extra["projects"].items():
            projects["mgmt/" + ns] = project
    else:
        print("ВНИМАНИЕ: MGMT_CONTEXT не задан. Счёт покрывает ДВА кластера, "
              "а ёмкость посчитана по одному — себестоимость завышена.")

    buckets = {k: [0.0, 0.0, 0.0] for k in ("TENANT", "OWN", "PLATFORM", "BOX")}
    tenants = []
    for ns, vals in namespaces.items():
        key = bucket_of(ns.split("/")[-1], {k.split("/")[-1]: v for k, v in projects.items()})
        for i in range(3):
            buckets[key][i] += vals[i]
        if key in ("TENANT", "OWN") and (vals[0] or vals[1] or vals[2]):
            tenants.append((ns, vals, key))
    tenants.sort(key=lambda t: -t[1][1])

    sellable = [cap[i] - buckets["PLATFORM"][i] for i in range(3)]
    if min(sellable) <= 0:
        sys.exit("ОСТАНОВ: платформа съела всю ёмкость по одному из измерений — "
                 "продавать нечего, и любая цена, выведенная отсюда, была бы выдумкой.")

    marginal = tuple(bill * WEIGHTS[i] / cap[i] for i in range(3))
    loaded = tuple(bill * WEIGHTS[i] / sellable[i] for i in range(3))

    print("СЧЁТ: %.0f ₽/мес (задан вручную, единственное число не из кластера)" % bill)
    print("\nЁМКОСТЬ (allocatable, live):")
    print("  всего        %6.1f vCPU  %6.1f ГиБ  %6.0f Gi" % tuple(cap))
    for key, label in (("PLATFORM", "платформа"), ("BOX", "боксы"),
                       ("OWN", "наши проекты"), ("TENANT", "тенанты")):
        b = buckets[key]
        print("  %-12s %6.2f vCPU  %6.2f ГиБ  %6.0f Gi   (%4.1f%% CPU, %4.1f%% RAM)"
              % (label, b[0], b[1], b[2], 100 * b[0] / cap[0], 100 * b[1] / cap[1]))
    print("  продаваемо   %6.2f vCPU  %6.2f ГиБ  %6.0f Gi" % tuple(sellable))

    print("\nСЕБЕСТОИМОСТЬ ₽/мес (веса CPU %.2f / RAM %.2f / Disk %.2f):" % WEIGHTS)
    print("  маржинальная  vCPU %7.1f   ГиБ RAM %7.1f   ГиБ диск %6.1f" % marginal)
    print("  загруженная   vCPU %7.1f   ГиБ RAM %7.1f   ГиБ диск %6.1f   ← пол цены" % loaded)

    print("\nРАБОЧИЕ НАГРУЗКИ (%d; O = наш проект, счёт себе не выставим):" % len(tenants))
    print("  %-36s %6s %7s %7s | %8s %8s | %-12s %7s"
          % ("namespace", "vCPU", "ГиБ", "Gi", "с/с загр", "наш ₽", "Amvera", "их ₽"))
    total_cost = total_ours = total_amvera = 0.0
    own_cost = 0.0
    for ns, (vcpu, ram, disk), kind in tenants:
        cost = vcpu * loaded[0] + ram * loaded[1] + disk * loaded[2]
        price, metered = our_price(vcpu, ram, disk)
        tier, tier_price = amvera_tier(vcpu, ram)
        floored = "*" if metered < PRICE_MIN_INVOICE else " "
        mark = "O " if kind == "OWN" else "  "
        print("  %s%-34s %6.2f %7.2f %7.0f | %8.1f %7.0f%s | %-12s %7.0f"
              % (mark, ns, vcpu, ram, disk, cost, price, floored, tier, tier_price))
        if kind == "OWN":
            own_cost += cost
            continue
        total_cost += cost
        total_ours += price
        total_amvera += tier_price
    print("  %-36s %6s %7s %7s | %8.1f %8.0f | %-12s %7.0f"
          % ("ИТОГО внешние", "", "", "", total_cost, total_ours, "", total_amvera))
    print("  * счёт поднят до минималки %.0f ₽" % PRICE_MIN_INVOICE)
    print("  наши проекты жгут %.0f ₽/мес и не приносят ничего" % own_cost)
    box_cost = sum(buckets["BOX"][i] * loaded[i] for i in range(3))
    print("  бокс-пул держит %.0f ₽/мес ёмкости (%.1f%% счёта) — сравни с внешним спросом на боксы"
          % (box_cost, 100 * box_cost / bill))

    print("\nПОКРЫТИЕ СЧЁТА (только внешние тенанты):")
    for label, value in (("по нашему прайсу", total_ours),
                         ("по прайсу Amvera (только compute)", total_amvera)):
        verdict = "ПОКРЫВАЕТ" if value >= bill else "НЕ покрывает"
        print("  %-34s %8.0f ₽  = %5.1f%% счёта  → %s"
              % (label, value, 100 * value / bill, verdict))

    print("\nСКОЛЬКО НАДО ЗАПОЛНИТЬ (продаваемо %.1f ГиБ RAM):" % sellable[1])
    for avg, label in ((0.125, "бот 128 MiB"), (0.5, "сервис 512 MiB"),
                       (1.0, "приложение 1 ГиБ")):
        fits = int(sellable[1] / avg)
        price, _ = our_price(avg / 2, avg, 1.0)
        revenue = fits * price
        need = int(bill / price) + 1
        print("  %-20s влезает %4d × %4.0f ₽ = %7.0f ₽/мес | безубыток с %d шт (%.0f%% пула)"
              % (label, fits, price, revenue, need, 100 * need / max(fits, 1)))

    revenue_arm(os.environ.get("DATABASE_URL"))


if __name__ == "__main__":
    main()
