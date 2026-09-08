"""Reply-ledger rules shared by chat agents.

The ledger answers one question after the fact: did the agent actually say
something to a human, or did it stay silent. That only works if a row marked
``answered`` carries the text that was said.

The first live A2A run of tg-vibecoder wrote ``decision=answered`` with an
empty ``reply`` and returned the single word "Ответил." to its caller: the
model had treated the logging tool as the delivery channel. The ledger then
claimed a reply that no human ever saw, and the hourly budget was spent on
nothing. Refusing that write is cheaper than auditing Telegram to find out.
"""

DECISION_ANSWERED = "answered"
DECISION_SKIPPED = "skipped"
DECISIONS = (DECISION_ANSWERED, DECISION_SKIPPED)


def validate_decision(decision: str, reply: str) -> str | None:
    """Return an error message for a rejected ledger write, or None when it is allowed."""
    if decision not in DECISIONS:
        return "decision должен быть answered или skipped"
    if decision == DECISION_ANSWERED and not (reply or "").strip():
        return "decision=answered требует текст в поле reply: журнал без текста не отличить от молчания"
    return None
