import os
import sys
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)

import ledger


class TestValidateDecision(unittest.TestCase):
    def test_answered_with_text_is_allowed(self):
        self.assertIsNone(ledger.validate_decision("answered", "у openai на этой неделе только пост про агентов"))

    def test_answered_without_text_is_refused(self):
        err = ledger.validate_decision("answered", "")
        self.assertIsNotNone(err)
        self.assertIn("reply", err)

    def test_answered_with_whitespace_only_is_refused(self):
        self.assertIsNotNone(ledger.validate_decision("answered", "   \n "))

    def test_skipped_needs_no_text(self):
        self.assertIsNone(ledger.validate_decision("skipped", ""))

    def test_unknown_decision_is_refused(self):
        self.assertIsNotNone(ledger.validate_decision("maybe", "текст"))

class TestSilenceSentinel(unittest.TestCase):
    def test_sentinel_recognised(self):
        for text in ("SKIP", " skip ", "Skip\n"):
            with self.subTest(text=text):
                self.assertTrue(ledger.is_silence(text))

    def test_real_reply_is_not_silence(self):
        self.assertFalse(ledger.is_silence("skip connection pooling, оно тут не поможет"))
        self.assertFalse(ledger.is_silence(""))


if __name__ == "__main__":
    unittest.main()
