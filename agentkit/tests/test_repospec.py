import json
import os
import shutil
import sys
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)

import repospec

VALID_ENTRY = {
    "name": "vibecoder",
    "agents_root": "tg-vibecoder",
    "cases": "tg-vibecoder/evals/persona/cases.jsonl",
    "holdout_threshold": 0.75,
}


def write_manifest(repo, payload):
    os.makedirs(os.path.join(repo, ".dada"), exist_ok=True)
    with open(os.path.join(repo, ".dada", "agent.json"), "w", encoding="utf-8") as handle:
        json.dump(payload, handle)


class TestLoadManifest(unittest.TestCase):
    def setUp(self):
        self.repo = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.repo)

    def test_valid_manifest_returns_entries(self):
        write_manifest(self.repo, {"version": 1, "agents": [VALID_ENTRY]})
        entries = repospec.load_manifest(self.repo)
        self.assertEqual(len(entries), 1)
        self.assertEqual(entries[0]["name"], "vibecoder")

    def test_missing_manifest_is_refused(self):
        with self.assertRaises(repospec.ManifestError) as ctx:
            repospec.load_manifest(self.repo)
        self.assertIn("declares no agent", str(ctx.exception))

    def test_broken_json_is_refused(self):
        os.makedirs(os.path.join(self.repo, ".dada"))
        with open(os.path.join(self.repo, ".dada", "agent.json"), "w", encoding="utf-8") as handle:
            handle.write("{not json")
        with self.assertRaises(repospec.ManifestError):
            repospec.load_manifest(self.repo)

    def test_unknown_version_is_refused(self):
        write_manifest(self.repo, {"version": 2, "agents": [VALID_ENTRY]})
        with self.assertRaises(repospec.ManifestError) as ctx:
            repospec.load_manifest(self.repo)
        self.assertIn("version", str(ctx.exception))

    def test_empty_agent_list_is_refused(self):
        write_manifest(self.repo, {"version": 1, "agents": []})
        with self.assertRaises(repospec.ManifestError):
            repospec.load_manifest(self.repo)

    def test_missing_required_key_is_refused(self):
        for key in repospec.REQUIRED_KEYS:
            entry = dict(VALID_ENTRY)
            del entry[key]
            write_manifest(self.repo, {"version": 1, "agents": [entry]})
            with self.subTest(key=key):
                with self.assertRaises(repospec.ManifestError) as ctx:
                    repospec.load_manifest(self.repo)
                self.assertIn(key, str(ctx.exception))

    def test_duplicate_agent_name_is_refused(self):
        write_manifest(self.repo, {"version": 1, "agents": [VALID_ENTRY, dict(VALID_ENTRY)]})
        with self.assertRaises(repospec.ManifestError) as ctx:
            repospec.load_manifest(self.repo)
        self.assertIn("duplicate", str(ctx.exception))

    def test_threshold_out_of_range_is_refused(self):
        for bad in (0.0, 0.2, 1.5):
            entry = dict(VALID_ENTRY, holdout_threshold=bad)
            write_manifest(self.repo, {"version": 1, "agents": [entry]})
            with self.subTest(threshold=bad):
                with self.assertRaises(repospec.ManifestError):
                    repospec.load_manifest(self.repo)

    def test_non_numeric_threshold_is_refused(self):
        for bad in ("0.75", True, None):
            entry = dict(VALID_ENTRY, holdout_threshold=bad)
            write_manifest(self.repo, {"version": 1, "agents": [entry]})
            with self.subTest(threshold=bad):
                with self.assertRaises(repospec.ManifestError):
                    repospec.load_manifest(self.repo)


class TestThisRepoSatisfiesTheSpec(unittest.TestCase):
    """The spec we hand to clients has to hold for the repo that wrote it."""

    def test_own_manifest_passes_the_gate(self):
        repo = os.path.dirname(ROOT)
        entries = repospec.load_manifest(repo)
        for entry in entries:
            for title, problems in repospec.check_agent(repo, entry):
                with self.subTest(agent=entry["name"], section=title):
                    self.assertEqual(problems, [])


if __name__ == "__main__":
    unittest.main()
