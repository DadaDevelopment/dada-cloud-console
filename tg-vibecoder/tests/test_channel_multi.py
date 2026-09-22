"""channel_ingest multi-channel rollout: env parsing and batch semantics."""

import os
import sys
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import channel_ingest


class SlugsFromEnvTest(unittest.TestCase):
    def test_slugs_list_wins_over_single(self):
        with mock.patch.dict(os.environ, {"CHANNEL_SLUGS": "a, b ,c", "CHANNEL_SLUG": "z"}):
            self.assertEqual(channel_ingest.slugs_from_env(), ["a", "b", "c"])

    def test_single_falls_back(self):
        with mock.patch.dict(os.environ, {"CHANNEL_SLUG": "vibe_architect_ai"}, clear=False):
            os.environ.pop("CHANNEL_SLUGS", None)
            self.assertEqual(channel_ingest.slugs_from_env(), ["vibe_architect_ai"])

    def test_empty(self):
        env = {"CHANNEL_SLUGS": "", "CHANNEL_SLUG": ""}
        with mock.patch.dict(os.environ, env, clear=False):
            os.environ.pop("CHANNEL_SLUG", None)
            os.environ.pop("CHANNEL_SLUGS", None)
            self.assertEqual(channel_ingest.slugs_from_env(), [])


class RunAllTest(unittest.IsolatedAsyncioTestCase):
    async def test_one_dead_channel_never_fails_batch(self):
        async def fake_run(slug, pages, timeout, dry_run):
            return 1 if slug == "dead" else 0

        with mock.patch.object(channel_ingest, "run", fake_run):
            rc = await channel_ingest.run_all(["dead", "alive"], 1, 1.0, False)
        self.assertEqual(rc, 0)

    async def test_all_dead_fails(self):
        async def fake_run(slug, pages, timeout, dry_run):
            return 1

        with mock.patch.object(channel_ingest, "run", fake_run):
            rc = await channel_ingest.run_all(["dead", "dead2"], 1, 1.0, False)
        self.assertEqual(rc, 1)

    async def test_exception_counts_as_failure(self):
        async def fake_run(slug, pages, timeout, dry_run):
            if slug == "boom":
                raise RuntimeError("kaboom")
            return 0

        with mock.patch.object(channel_ingest, "run", fake_run):
            rc = await channel_ingest.run_all(["boom", "alive"], 1, 1.0, False)
        self.assertEqual(rc, 0)


if __name__ == "__main__":
    unittest.main()
