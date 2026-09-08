"""The python side of the inbound-shape contract, pinned by the shared golden file."""

import json
import os
import sys
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.dirname(ROOT))

from agentkit import transcript

GOLDEN = os.path.join(ROOT, "transcript_golden.json")


class TestGolden(unittest.TestCase):
    def setUp(self):
        with open(GOLDEN, encoding="utf-8") as fh:
            self.cases = json.load(fh)["cases"]

    def test_every_golden_case_round_trips(self):
        self.assertTrue(self.cases)
        for case in self.cases:
            with self.subTest(case["name"]):
                spk = transcript.speaker(case["first_name"], case["username"])
                quoted = transcript.quoted_context(
                    case["quoted_text"],
                    case["quoted_is_channel"],
                    case["quoted_username"],
                )
                self.assertEqual(spk, case["want_speaker"])
                self.assertEqual(quoted, case["want_quoted"])
                self.assertEqual(
                    transcript.inbound(case["text"], spk, quoted), case["want_inbound"]
                )


class TestEdges(unittest.TestCase):
    def test_long_quote_is_cut_and_marked(self):
        quoted = transcript.quoted_context("а" * 500, is_channel=True)
        self.assertIn("...", quoted)
        self.assertLess(len(quoted), 460)

    def test_blank_quote_disappears_entirely(self):
        self.assertEqual(transcript.quoted_context("   \n  "), "")
        self.assertEqual(transcript.inbound("привет", "Аня", ""), "Аня: привет")


if __name__ == "__main__":
    unittest.main()
