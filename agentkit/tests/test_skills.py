"""Domain loading must refuse path escapes by pattern, not by sanitizing."""

import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import skills


class TestSkillSet(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp()
        d = os.path.join(self.root, "agents", "bot", "domains")
        os.makedirs(d)
        with open(os.path.join(self.root, "agents", "bot", "core.md"), "w", encoding="utf-8") as fh:
            fh.write("core prompt, load debug when asked")
        for name in ("debug", "orphan"):
            with open(os.path.join(d, name + ".md"), "w", encoding="utf-8") as fh:
                fh.write(name + " body")
        self.s = skills.SkillSet(self.root, "bot")

    def test_load(self):
        self.assertEqual(self.s.load("debug"), "debug body")

    def test_listing(self):
        self.assertEqual(self.s.domains(), ["debug", "orphan"])

    def test_path_escape_rejected(self):
        for bad in ("../core", "..", "a/b", "/etc/passwd", "Debug", ""):
            with self.subTest(bad=bad), self.assertRaises(skills.SkillError):
                self.s.load(bad)

    def test_missing_domain_is_an_error_not_empty_string(self):
        with self.assertRaises(skills.SkillError):
            self.s.load("nope")

    def test_audit_finds_unreferenced_domain(self):
        audit = self.s.audit()
        self.assertEqual(audit["unreferenced_domains"], ["orphan"])

    def test_invalid_agent_name_rejected(self):
        with self.assertRaises(skills.SkillError):
            skills.SkillSet(self.root, "../etc")


if __name__ == "__main__":
    unittest.main()
