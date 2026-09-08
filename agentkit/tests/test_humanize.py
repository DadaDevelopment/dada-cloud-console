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


if __name__ == "__main__":
    unittest.main()
