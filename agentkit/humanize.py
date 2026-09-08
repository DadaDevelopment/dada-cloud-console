"""Mechanical detector for AI-tells in a chat-length reply.

Platform track 4 (human-изация). This is deliberately rule-based and model-free:
a slop check that needs an LLM cannot run in a pre-merge gate, cannot explain
itself, and drifts with the judge. Rules here are falsifiable — each one names
the exact span it fired on.

Two severities. ``critical`` means a human in a Telegram comment section would
read the message as machine-written; ``soft`` means it is merely bland. A gate
fails on critical only, so the soft list stays useful as advice instead of
becoming noise everyone learns to ignore.

Scope: short conversational replies. Do not point it at documentation, commit
messages, or code — bullet lists and headings are correct there.
"""

import re
import unicodedata

BANNED_PHRASES_CRITICAL = [
    "отличный вопрос",
    "хороший вопрос",
    "давайте разберёмся",
    "давайте разберемся",
    "давай разберёмся",
    "важно понимать",
    "важно отметить",
    "стоит отметить",
    "стоит учитывать",
    "надеюсь, это помогло",
    "надеюсь это помогло",
    "если есть вопросы",
    "если возникнут вопросы",
    "буду рад помочь",
    "рад помочь",
    "чем могу помочь",
    "подведём итог",
    "подведем итог",
    "таким образом",
    "в заключение",
    "great question",
    "let me break",
    "hope this helps",
    "feel free to ask",
    "it's important to note",
    "in conclusion",
]

BANNED_PHRASES_SOFT = [
    "в целом",
    "как правило",
    "в общем и целом",
    "ключевое отличие",
    "ключевой момент",
    "на самом деле",
    "по сути",
    "стоит помнить",
    "не просто",
    "тем не менее",
]

_EMOJI_RE = re.compile(
    "[" "\U0001f300-\U0001faff" "\U00002600-\U000027bf" "\U0001f000-\U0001f0ff" "⬀-⯿" "]"
)
_HEADING_RE = re.compile(r"^\s{0,3}#{1,6}\s+\S", re.M)
_BULLET_RE = re.compile(r"^\s{0,3}([-*•]|\d+[.)])\s+\S", re.M)
_BOLD_RE = re.compile(r"\*\*[^*]+\*\*|__[^_]+__")
_CODE_FENCE_RE = re.compile(r"```.*?```", re.S)
_HEDGE_WORDS = ["возможно", "вероятно", "скорее всего", "как будто", "наверное", "в принципе"]
_AI_DASHES = {"—", "–"}
NBH = "\u2011"
NBSP = "\u00a0"
SHY = "\u00ad"
ZWSP = "\u200b"
NNBSP = "\u202f"
WJ = "\u2060"
FIGSP = "\u2007"

_FORBIDDEN_UNICODE = {
    NBH: "non-breaking hyphen",
    NBSP: "non-breaking space",
    SHY: "soft hyphen",
    ZWSP: "zero-width space",
    NNBSP: "narrow no-break space",
    WJ: "word joiner",
    FIGSP: "figure space",
}


class Finding:
    """One rule hit, with the span that triggered it."""

    __slots__ = ("rule", "severity", "detail")

    def __init__(self, rule: str, severity: str, detail: str):
        self.rule = rule
        self.severity = severity
        self.detail = detail

    def __repr__(self) -> str:
        return f"Finding({self.rule!r}, {self.severity!r}, {self.detail!r})"

    def as_dict(self) -> dict:
        return {"rule": self.rule, "severity": self.severity, "detail": self.detail}


def _strip_code(text: str) -> str:
    return _CODE_FENCE_RE.sub(" ", text)


_STATUS_ONLY_RE = re.compile(
    r"^\W*(?:ок|окей|готово|сделано|сделал|ответил|ответила|отправил|отправила|записал|записала|"
    r"выполнено|принял|принято|done|ok|okay)\W*$",
    re.I,
)


_TOOL_OUTPUT_RE = re.compile(r'^\s*[\[{].*[\]}]\s*$', re.S)
_TOOL_KEYS_RE = re.compile(r'"(?:ok|error|id|found|domain|used_last_hour|remaining|rows|items)"\s*:', re.I)


def _tool_output(prose: str) -> bool:
    """True when the reply is a serialized tool result rather than a sentence.

    Same root as ``_status_only``: the model stops writing after its last tool
    call, and the runtime hands the caller that call's JSON. It reads as a
    reply to every layer that only checks for a non-empty string.
    """
    stripped = prose.strip()
    return bool(_TOOL_OUTPUT_RE.match(stripped) and _TOOL_KEYS_RE.search(stripped))


def _status_only(prose: str) -> bool:
    """True when the whole reply is a report about replying instead of the reply.

    A model that is given a logging tool sometimes decides the tool is the
    delivery channel and answers its caller with "Ответил." The transport is
    green, the ledger has a row, and the human got nothing. Cheap to catch on
    the text, so it is caught here rather than in a postmortem.
    """
    return bool(_STATUS_ONLY_RE.match(prose.strip()))


def inspect(text: str, max_chars: int = 700, max_sentences: int = 5) -> list[Finding]:
    """Return every rule hit in ``text``, critical ones first.

    ``max_chars``/``max_sentences`` are the chat-comment budget; a reply longer
    than that reads as a blog post no matter how good the wording is.
    """
    findings: list[Finding] = []
    prose = _strip_code(text)
    low = prose.lower()

    if _status_only(prose):
        findings.append(Finding("status_instead_of_reply", "critical", prose.strip()))
    if _tool_output(prose):
        findings.append(Finding("tool_output_as_reply", "critical", prose.strip()[:80]))

    for phrase in BANNED_PHRASES_CRITICAL:
        if phrase in low:
            findings.append(Finding("banned_phrase", "critical", phrase))
    for phrase in BANNED_PHRASES_SOFT:
        if phrase in low:
            findings.append(Finding("filler_phrase", "soft", phrase))

    for char, name in _FORBIDDEN_UNICODE.items():
        if char in text:
            findings.append(Finding("forbidden_unicode", "critical", name))

    dashes = sum(prose.count(d) for d in _AI_DASHES)
    if dashes:
        findings.append(Finding("em_dash", "critical", f"{dashes} длинных тире"))

    if _HEADING_RE.search(prose):
        findings.append(Finding("markdown_heading", "critical", "заголовок в реплике чата"))

    bullets = _BULLET_RE.findall(prose)
    if len(bullets) >= 2:
        findings.append(Finding("bullet_list", "critical", f"{len(bullets)} пунктов списка"))

    if _BOLD_RE.search(prose):
        findings.append(Finding("bold_markup", "soft", "жирный шрифт"))

    emoji = _EMOJI_RE.findall(prose)
    if len(emoji) > 1:
        findings.append(Finding("emoji_spam", "critical", f"{len(emoji)} эмодзи"))

    hedges = [w for w in _HEDGE_WORDS if w in low]
    if len(hedges) >= 2:
        findings.append(Finding("hedge_stack", "soft", ", ".join(hedges)))

    if len(prose.strip()) > max_chars:
        findings.append(Finding("too_long", "critical", f"{len(prose.strip())} символов"))

    sentences = [s for s in re.split(r"[.!?]+\s", prose.strip()) if s.strip()]
    if len(sentences) > max_sentences:
        findings.append(Finding("too_many_sentences", "soft", f"{len(sentences)} предложений"))

    stripped = prose.strip()
    if stripped.endswith("?") and len(sentences) > 1:
        findings.append(Finding("trailing_question", "soft", "закрывающий вопрос ради диалога"))

    findings.sort(key=lambda f: 0 if f.severity == "critical" else 1)
    return findings


def is_clean(text: str, **kwargs) -> bool:
    """True when nothing critical fired."""
    return not any(f.severity == "critical" for f in inspect(text, **kwargs))


def report(text: str, **kwargs) -> dict:
    findings = inspect(text, **kwargs)
    return {
        "clean": not any(f.severity == "critical" for f in findings),
        "critical": [f.as_dict() for f in findings if f.severity == "critical"],
        "soft": [f.as_dict() for f in findings if f.severity == "soft"],
    }


def strip_forbidden_unicode(text: str) -> str:
    """Replace the typography AI-tells with their ASCII equivalents."""
    out = text.replace(NBH, "-")
    for char in (NBSP, NNBSP, FIGSP):
        out = out.replace(char, " ")
    for char in (SHY, ZWSP, WJ):
        out = out.replace(char, "")
    return unicodedata.normalize("NFC", out)
