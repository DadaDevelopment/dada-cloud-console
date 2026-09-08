"""The shape of an inbound group message, as the platform transport builds it.

A model in a group chat does not get a bare comment. It gets who spoke and,
when the comment answers something, what was quoted. That assembly lives in
``backend/internal/tggateway`` (``GroupSpeaker``, ``QuotedContext`` and the
batch loop of ``manager.go``), and until now nothing outside Go knew about it:
the persona eval fed the model ``Пост канала: ...\\n\\nКомментарий: ...``, a
shape that never reaches production. An eval that grades a different input than
the transport delivers grades a different agent.

So the rule is written once, here, and the two runtimes are pinned to the same
answers by ``transcript_golden.json``: python reads it in
``agentkit/tests/test_transcript.py``, Go reads it in
``backend/internal/tggateway/transcript_golden_test.go``. Change the format on
one side and the other side goes red.
"""

QUOTED_LIMIT = 400

SOURCE_CHANNEL = "пост канала"
SOURCE_DEFAULT = "сообщение"


def speaker(first_name: str = "", username: str = "") -> str:
    """Name the author the way the transport names them: name plus @handle when there is one."""
    name = (first_name or "").strip() or (username or "").strip() or "аноним"
    if (username or "").strip():
        return f"{name} (@{username.strip()})"
    return name


def quoted_context(text: str, is_channel: bool = False, username: str = "") -> str:
    """Render the bracketed line that carries the quoted message, or an empty string."""
    quoted = (text or "").strip()
    if not quoted:
        return ""
    runes = list(quoted)
    if len(runes) > QUOTED_LIMIT:
        quoted = "".join(runes[:QUOTED_LIMIT]) + "..."
    quoted = " ".join(quoted.split())
    if is_channel:
        source = SOURCE_CHANNEL
    elif (username or "").strip():
        source = "@" + username.strip()
    else:
        source = SOURCE_DEFAULT
    return f"[в ответ на {source}: {_go_quote(quoted)}]"


def inbound(text: str, speaker_line: str = "", quoted_line: str = "") -> str:
    """Assemble the message content exactly as the transport hands it to the runtime."""
    content = text or ""
    if speaker_line:
        content = f"{speaker_line}: {content}"
    if quoted_line:
        content = f"{quoted_line}\n{content}"
    return content


def _go_quote(value: str) -> str:
    """Mirror Go's %q for the printable text that survives whitespace collapsing."""
    return '"' + value.replace("\\", "\\\\").replace('"', '\\"') + '"'
