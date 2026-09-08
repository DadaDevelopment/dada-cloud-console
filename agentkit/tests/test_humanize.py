"""Rules must fire on slop and stay quiet on real chat text."""

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import humanize


class TestCritical(unittest.TestCase):
    def test_banned_phrase_fires(self):
        rules = {f.rule for f in humanize.inspect("Отличный вопрос, сейчас гляну")}
        self.assertIn("banned_phrase", rules)

    def test_em_dash_fires(self):
        self.assertFalse(humanize.is_clean("это работает — но медленно"))

    def test_bullet_list_fires(self):
        text = "варианты:\n- первый\n- второй"
        rules = {f.rule for f in humanize.inspect(text)}
        self.assertIn("bullet_list", rules)

    def test_single_bullet_is_not_a_list(self):
        rules = {f.rule for f in humanize.inspect("сделай так:\n- поставь таймаут")}
        self.assertNotIn("bullet_list", rules)

    def test_heading_fires(self):
        rules = {f.rule for f in humanize.inspect("## Ответ\nставь таймаут")}
        self.assertIn("markdown_heading", rules)

    def test_emoji_spam_fires_only_above_one(self):
        self.assertTrue(humanize.is_clean("ну да, бывает 🙂"))
        self.assertFalse(humanize.is_clean("ну да, бывает 🙂🚀"))

    def test_forbidden_unicode_fires(self):
        self.assertFalse(humanize.is_clean("нон" + humanize.NBH + "брейкинг"))
        self.assertFalse(humanize.is_clean("нбсп" + humanize.NBSP + "тут"))

    def test_too_long_fires(self):
        self.assertFalse(humanize.is_clean("а" * 800))

    def test_code_fence_is_exempt_from_prose_rules(self):
        text = "вот так:\n```\n# comment\n- not a bullet\n- also not\n```"
        rules = {f.rule for f in humanize.inspect(text)}
        self.assertNotIn("bullet_list", rules)


class TestClean(unittest.TestCase):
    CLEAN = [
        "смотря какой размер контекста. на 200к у меня всё равно деградирует к концу",
        "не, это не про модель. у тебя таймаут на gateway 20 секунд, а ход идёт минуту",
        "проверял неделю назад, было так. сейчас не проверял",
        "ставь temperature 0 и прогоняй тот же промпт дважды. если ответы разные, дело не в промпте",
    ]

    def test_real_replies_pass(self):
        for text in self.CLEAN:
            with self.subTest(text=text[:30]):
                self.assertTrue(humanize.is_clean(text), humanize.report(text))


class TestSoft(unittest.TestCase):
    def test_hedge_stack_is_soft_not_critical(self):
        report = humanize.report("возможно дело в кеше, наверное")
        self.assertTrue(report["clean"])
        self.assertTrue(any(f["rule"] == "hedge_stack" for f in report["soft"]))

    def test_trailing_question_needs_more_than_one_sentence(self):
        rules = {f.rule for f in humanize.inspect("а ты какой рантайм гоняешь?")}
        self.assertNotIn("trailing_question", rules)


class TestStrip(unittest.TestCase):
    def test_strip_replaces_with_ascii(self):
        dirty = "а" + humanize.NBH + "б" + humanize.NBSP + "в" + humanize.ZWSP + "г"
        self.assertEqual(humanize.strip_forbidden_unicode(dirty), "а-б вг")

    def test_strip_output_passes_the_check(self):
        dirty = "тест" + humanize.NNBSP + "строки" + humanize.WJ
        self.assertTrue(humanize.is_clean(humanize.strip_forbidden_unicode(dirty)))


class TestToolOutputAsReply(unittest.TestCase):
    """A2A has no empty turn: the runtime hands the caller the last tool result.

    Live probe: a turn that decided to stay silent returned the literal text
    {"ok":true,"id":3}. Every layer that only checks for a non-empty string
    read that as a reply.
    """

    def test_serialized_tool_result_is_critical(self):
        for text in ('{"ok":true,"id":3}', '{"ok": false, "error": "no"}', '[{"id":1,"title":"x"}]'):
            with self.subTest(text=text):
                rules = [f.rule for f in humanize.inspect(text) if f.severity == "critical"]
                self.assertIn("tool_output_as_reply", rules)

    def test_prose_mentioning_braces_is_clean(self):
        rules = [f.rule for f in humanize.inspect("код в {} скобках это норм, если ok")]
        self.assertNotIn("tool_output_as_reply", rules)


if __name__ == "__main__":
    unittest.main()


class TestStatusInsteadOfReply(unittest.TestCase):
    def test_bare_status_word_is_critical(self):
        for text in ("Ответил.", "готово", "Отправил", "done", "ok"):
            with self.subTest(text=text):
                rules = [f["rule"] for f in humanize.report(text)["critical"]]
                self.assertIn("status_instead_of_reply", rules)

    def test_real_reply_starting_with_ok_passes(self):
        rules = [f["rule"] for f in humanize.report("ok, но retries тут не помогут: падает не сеть, а парсер")["critical"]]
        self.assertNotIn("status_instead_of_reply", rules)


class TestTransportLeak(unittest.TestCase):
    """The scaffolding the runtime prepends must never come back out."""

    def test_bracketed_quote_is_critical(self):
        reply = '[в ответ на пост канала: "агенты в комментах"] да, работает'
        rules = [f.rule for f in humanize.inspect(reply)]
        self.assertIn("transport_leak", rules)

    def test_speaker_prefix_is_critical(self):
        rules = [f.rule for f in humanize.inspect("Игорь (@igor_dev): да, работает")]
        self.assertIn("transport_leak", rules)

    def test_anon_prefix_is_critical(self):
        self.assertIn("transport_leak", [f.rule for f in humanize.inspect("аноним: работает")])

    def test_ordinary_reply_and_a_colon_survive(self):
        for reply in ("да, работает", "смотри так: сначала эвал, потом промпт", "у Игоря так же было"):
            with self.subTest(reply):
                self.assertNotIn("transport_leak", [f.rule for f in humanize.inspect(reply)])


class TestForeignScript(unittest.TestCase):
    """A token from the model's other language is the loudest machine tell there is."""

    def test_live_glm_bleed_is_critical(self):
        reply = 'платишь за каждый запрос заново и模型 всё равно ловит lost in the middle'
        self.assertIn("foreign_script", [f.rule for f in humanize.inspect(reply)])

    def test_plain_russian_and_english_survive(self):
        for reply in ("rag оставляет только релевантные куски", "смотри логи, там ECONNREFUSED"):
            with self.subTest(reply):
                self.assertNotIn("foreign_script", [f.rule for f in humanize.inspect(reply)])
