# LLM providers (OpenAI-compatible gateway)

## What it's for

Call GPT, Claude, Gemini and the rest from an application running in Russia, over one
OpenAI-compatible endpoint, without a VPN and without a proxy of your own. You point any SDK
that speaks OpenAI at the gateway base URL, authenticate with a project key, and the platform
routes the request to the upstream provider.

## The one decision: whose provider key pays

Open **LLM providers** in the project nav and you are asked this first, because everything
after it is the same.

### Your own keys

You register an OpenAI, Anthropic or other provider key in the project. The gateway injects it
into every matching call.

- You need an account with that provider and a way to pay it — usually a foreign card.
- The provider invoices you directly. The gateway itself costs nothing.
- Your key is stored encrypted and is never returned to the browser after it is saved.

### Our keys

The call runs on the platform's own provider key. Nothing to register.

- Zero setup: it works the moment you switch the project over.
- You pay us for routing, by tokens actually spent, at a markup on the provider price. It lands
  on the same invoice as the rest of your cloud, payable with a Russian card.
- Switching a project into this mode is what starts the meter. A project that never switches is
  never charged for routing.

You can move between the two at any time. The switch is recorded in the project audit trail,
including who made it, so a disputed invoice has an answer.

## Getting a call working

1. **Create a key.** On the same page, mint an `sk-dada-ai-…` project key. It is shown once —
   copy it then. Hand separate keys to separate services so you can revoke one without
   breaking the others.
2. **Choose the key mode** above: add a provider key, or switch the project onto ours.
3. **Call a model** using the copy-paste snippet on the page (Python, Node.js or curl). It is
   rendered with your real base URL and model alias, so it runs as-is.

Use the model *alias* from the catalog table (for example `fast`, `medium`, `smart`, or a
provider-specific alias), not the upstream vendor name. Aliases keep working when the model
behind them is replaced.

## What you can see afterwards

The usage block on the page shows, for the trailing 7 or 30 days: calls, tokens, what the
traffic cost, and — in managed mode — what you owe for routing. The per-model table breaks the
same window down by model, so an unexpected bill has a specific line to look at.

## Related

- [AI Models (model serving)](/en/developer/ai-models) — running *your own* model on a GPU, which
  is a different product: that one serves weights you supply, this one routes to a vendor.
- [Billing, plans, and limits](/en/developer/billing-plans-and-limits)
