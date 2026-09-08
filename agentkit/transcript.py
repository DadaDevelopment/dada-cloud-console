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

CHANNEL_POST_MARKER = "[новый пост в канале]"

MEDIA_LIMIT = 600

MEDIA_LABELS = {
    "image": "изображение",
    "voice": "голосовое",
    "video_note": "кружок",
    "document": "файл",
    "video": "видео",
}

MEDIA_UNAVAILABLE = {
    "image": "описание недоступно",
    "voice": "расшифровка недоступна",
    "video_note": "расшифровка недоступна",
}


def speaker(first_name: str = "", username: str = "", is_channel_post: bool = False) -> str:
    """Name the author the way the transport names them: name plus @handle when there is one.

    The channel's own post has no author to name: telegram delivers it as an
    automatic forward, and prefixing it with whatever name rides along would
    tell the model a person said it.
    """
    if is_channel_post:
        return ""
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


def media_context(
    kind: str = "",
    description: str = "",
    transcript_text: str = "",
    file_name: str = "",
) -> str:
    """Render the bracketed line that carries an attachment, or an empty string.

    A screenshot is the normal way this audience asks a question: the post is a
    picture of a settings screen and the comment under it is "а что там внизу
    написано". Text alone drops the whole question. The transport resolves the
    picture to words (vision) or the voice message to a transcript, and this
    line is where those words enter the message.

    An attachment whose content could not be resolved still gets a line: the
    model must know a picture was there and that it did not get to see it,
    which is a different answer than pretending the message was empty.
    """
    kind = (kind or "").strip()
    if not kind:
        return ""
    label = MEDIA_LABELS.get(kind, kind)
    body = (description or transcript_text or "").strip()
    if body:
        runes = list(body)
        if len(runes) > MEDIA_LIMIT:
            body = "".join(runes[:MEDIA_LIMIT]) + "..."
        body = " ".join(body.split())
    elif kind == "document":
        body = (file_name or "").strip()
    else:
        body = MEDIA_UNAVAILABLE.get(kind, "содержимое недоступно")
    if not body:
        return f"[{label}]"
    return f"[{label}: {body}]"


def inbound(
    text: str,
    speaker_line: str = "",
    quoted_line: str = "",
    is_channel_post: bool = False,
    media_line: str = "",
) -> str:
    """Assemble the message content exactly as the transport hands it to the runtime."""
    if is_channel_post:
        head = CHANNEL_POST_MARKER
        if media_line:
            head = f"{head}\n{media_line}"
        return f"{head}\n{text or ''}".rstrip()
    content = text or ""
    if speaker_line:
        content = f"{speaker_line}: {content}"
    if media_line:
        content = f"{media_line}\n{content}"
    if quoted_line:
        content = f"{quoted_line}\n{content}"
    return content.rstrip()


def _go_quote(value: str) -> str:
    """Mirror Go's %q for the printable text that survives whitespace collapsing."""
    return '"' + value.replace("\\", "\\\\").replace('"', '\\"') + '"'
