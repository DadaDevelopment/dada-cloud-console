"""Freshness rules must fire on a flat claim and stand down on a hedged one."""

import os
import sys
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(ROOT, "scripts"))

import persona_lint


class TestFreshness(unittest.TestCase):
    def test_bare_version_is_critical(self):
        rules = {f["rule"] for f in persona_lint.lint("бери gpt 5 и не думай")["critical"]}
        self.assertIn("unsourced_version", rules)

    def test_hedged_version_passes(self):
        self.assertTrue(persona_lint.lint("не проверял, но вроде gpt 5 уже был")["clean"])

    def test_sourced_flag_stands_the_rule_down(self):
        self.assertFalse(persona_lint.lint("вышел claude 4.5")["clean"])
        self.assertTrue(persona_lint.lint("вышел claude 4.5", sourced=True)["clean"])

    def test_price_claim(self):
        rules = {f["rule"] for f in persona_lint.lint("это $3 за млн токенов")["critical"]}
        self.assertIn("unsourced_price", rules)

    def test_context_window_claim(self):
        rules = {f["rule"] for f in persona_lint.lint("там 200к контекста")["critical"]}
        self.assertIn("unsourced_limit", rules)

    def test_recency_claim(self):
        rules = {f["rule"] for f in persona_lint.lint("они это вчера релизнули")["critical"]}
        self.assertIn("unsourced_recency", rules)

    def test_ordinary_opinion_passes(self):
        clean = [
            "у меня на длинных рефакторах терминальный агент стабильнее, чем ide-шный",
            "проблема не в модели, а в том что ты грузишь весь репозиторий в контекст",
        ]
        for text in clean:
            with self.subTest(text=text[:30]):
                self.assertTrue(persona_lint.lint(text)["clean"], persona_lint.lint(text))


class TestIdentity(unittest.TestCase):
    def test_denying_bothood_is_critical(self):
        rules = {f["rule"] for f in persona_lint.lint("да я не бот, обычный человек")["critical"]}
        self.assertIn("false_human_claim", rules)

    def test_admitting_it_passes(self):
        self.assertTrue(persona_lint.lint("бот я, канал меня и держит")["clean"])


class TestAdapter(unittest.TestCase):
    def test_style_failures_returns_strings_for_the_eval_runner(self):
        failures = persona_lint.style_failures("Отличный вопрос! там 200к контекста")
        self.assertTrue(failures)
        self.assertTrue(all(isinstance(f, str) for f in failures))

    def test_clean_reply_yields_no_failures(self):
        self.assertEqual(persona_lint.style_failures("не проверял, гляну"), [])


if __name__ == "__main__":
    unittest.main()
