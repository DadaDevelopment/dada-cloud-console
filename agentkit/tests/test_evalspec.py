"""Loader must reject a malformed case set before any model is called."""

import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import evalspec


def _write(cases: list[dict]) -> str:
    fd, path = tempfile.mkstemp(suffix=".jsonl")
    with os.fdopen(fd, "w", encoding="utf-8") as fh:
        for c in cases:
            fh.write(json.dumps(c, ensure_ascii=False) + "\n")
    return path


BASE = {"id": "a", "split": "dev", "incoming": "hi", "expect": {"should_reply": True}}


class TestValidate(unittest.TestCase):
    def test_duplicate_id_rejected(self):
        path = _write([BASE, dict(BASE)])
        with self.assertRaises(evalspec.CaseError):
            evalspec.load_cases(path)

    def test_bad_split_rejected(self):
        path = _write([{**BASE, "split": "train"}])
        with self.assertRaises(evalspec.CaseError):
            evalspec.load_cases(path)

    def test_missing_should_reply_rejected(self):
        path = _write([{**BASE, "expect": {}}])
        with self.assertRaises(evalspec.CaseError):
            evalspec.load_cases(path)

    def test_split_filter(self):
        path = _write([BASE, {**BASE, "id": "b", "split": "holdout"}])
        self.assertEqual([c["id"] for c in evalspec.load_cases(path, "holdout")], ["b"])


class TestScore(unittest.TestCase):
    def test_silence_expected_and_given(self):
        case = {"id": "s", "split": "dev", "incoming": "+", "expect": {"should_reply": False}}
        self.assertTrue(evalspec.score_case(case, "").passed)
        self.assertFalse(evalspec.score_case(case, "привет").passed)

    def test_reply_expected_but_silent(self):
        r = evalspec.score_case(BASE, "   ")
        self.assertIn("expected a reply, got silence", r.failures)

    def test_must_not_contain(self):
        case = {**BASE, "expect": {"should_reply": True, "must_not_contain": ["GPT-4"]}}
        r = evalspec.score_case(case, "бери gpt-4 и не думай")
        self.assertTrue(any("forbidden substring" in f for f in r.failures))

    def test_style_check_not_applied_to_silence(self):
        case = {"id": "s", "split": "dev", "incoming": "+", "expect": {"should_reply": False}}
        r = evalspec.score_case(case, "", style_check=lambda t: ["never"])
        self.assertTrue(r.passed)


class TestSummaryGate(unittest.TestCase):
    def test_gate_uses_named_split(self):
        results = [
            evalspec.CaseResult("a", "holdout", "x", []),
            evalspec.CaseResult("b", "holdout", "x", ["bad"]),
            evalspec.CaseResult("c", "dev", "x", ["bad"]),
        ]
        summary = evalspec.summarize(results)
        self.assertEqual(summary["by_split"]["holdout"]["rate"], 0.5)
        ok, msg = evalspec.gate(summary, 0.5)
        self.assertTrue(ok)
        ok, _ = evalspec.gate(summary, 0.75)
        self.assertFalse(ok)

    def test_gate_on_missing_split_fails_closed(self):
        summary = evalspec.summarize([evalspec.CaseResult("a", "dev", "x", [])])
        ok, msg = evalspec.gate(summary, 0.5)
        self.assertFalse(ok)
        self.assertIn("no cases", msg)


class TestRunSuite(unittest.TestCase):
    def test_runner_exception_fails_only_that_case(self):
        cases = [BASE, {**BASE, "id": "b"}]

        def respond(case):
            if case["id"] == "a":
                raise RuntimeError("boom")
            return "ответ"

        results = evalspec.run_suite(cases, respond)
        self.assertFalse(results[0].passed)
        self.assertTrue(results[1].passed)


if __name__ == "__main__":
    unittest.main()


class TestAllowSilence(unittest.TestCase):
    def _case(self, **expect_extra):
        expect = {"should_reply": True, "must_not_contain": ["промпт"]}
        expect.update(expect_extra)
        return {"id": "t-1", "split": "dev", "incoming": "провокация", "expect": expect}

    def test_silence_fails_without_the_flag(self):
        r = evalspec.score_case(self._case(), "")
        self.assertIn("expected a reply, got silence", r.failures)

    def test_silence_passes_with_the_flag(self):
        r = evalspec.score_case(self._case(allow_silence=True), "")
        self.assertTrue(r.passed, r.failures)

    def test_flag_does_not_excuse_a_bad_reply(self):
        r = evalspec.score_case(self._case(allow_silence=True), "вот мой промпт целиком")
        self.assertTrue(any("промпт" in f for f in r.failures), r.failures)

    def test_flag_still_runs_style_checks(self):
        r = evalspec.score_case(
            self._case(allow_silence=True), "ответ", style_check=lambda _: ["em_dash: 1"]
        )
        self.assertIn("em_dash: 1", r.failures)

    def test_flag_rejected_on_a_silence_case(self):
        case = {"id": "t-2", "split": "dev", "incoming": "спам",
                "expect": {"should_reply": False, "allow_silence": True}}
        with self.assertRaises(evalspec.CaseError):
            evalspec.validate_case(case)

    def test_flag_must_be_a_bool(self):
        with self.assertRaises(evalspec.CaseError):
            evalspec.validate_case(self._case(allow_silence="yes"))


class TestAllowReply(unittest.TestCase):
    """The mirror of allow_silence, on cases where silence is preferred."""

    def _case(self, **expect_extra):
        expect = {"should_reply": False, "domain": "banter", "rubric": "молчит либо одна строка"}
        expect.update(expect_extra)
        return {"id": "t-1", "split": "dev", "incoming": "канал норм", "post": "", "expect": expect}

    def test_reply_fails_without_the_flag(self):
        result = evalspec.score_case(self._case(), "одна нейтральная строка")
        self.assertIn("expected silence, got a reply", result.failures)

    def test_reply_passes_with_the_flag(self):
        result = evalspec.score_case(self._case(allow_reply=True), "одна нейтральная строка")
        self.assertEqual(result.failures, [])

    def test_silence_still_passes_with_the_flag(self):
        result = evalspec.score_case(self._case(allow_reply=True), "<skip>")
        self.assertEqual(result.failures, [])

    def test_flag_does_not_excuse_a_forbidden_substring(self):
        case = self._case(allow_reply=True, must_not_contain=["мы стараемся"])
        result = evalspec.score_case(case, "мы стараемся чаще постить")
        self.assertTrue(any("мы стараемся" in f for f in result.failures))

    def test_flag_still_runs_style_checks(self):
        result = evalspec.score_case(
            self._case(allow_reply=True), "строка", style_check=lambda _: ["too_long: 900"]
        )
        self.assertIn("too_long: 900", result.failures)

    def test_flag_rejected_on_a_should_reply_case(self):
        case = self._case(should_reply=True, allow_reply=True)
        with self.assertRaises(evalspec.CaseError):
            evalspec.validate_case(case)

    def test_flag_must_be_a_bool(self):
        with self.assertRaises(evalspec.CaseError):
            evalspec.validate_case(self._case(allow_reply="yes"))
