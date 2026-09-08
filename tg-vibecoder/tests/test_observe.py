"""The intake that turns other people's comments into the agent's memory."""

import os
import sys
import types
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)

sys.modules.setdefault("asyncpg", types.ModuleType("asyncpg"))
sys.modules["asyncpg"].Pool = object

os.environ.setdefault("MCP_AUTH_TOKEN", "test-token")

from starlette.testclient import TestClient

import search_sql
import server
import storage


class TestObserveRoute(unittest.TestCase):
    def setUp(self):
        self.seen = []

        async def fake_record(observation):
            self.seen.append(observation)
            return "INSERT 0 1"

        self.original = storage.record_comment
        storage.record_comment = fake_record
        self.client = TestClient(server.build_app())
        self.auth = {"Authorization": "Bearer test-token"}

    def tearDown(self):
        storage.record_comment = self.original

    def test_a_skipped_comment_is_still_stored(self):
        body = {
            "agent": "tg-vibecoder",
            "chat_id": -1002171703932,
            "message_id": 41,
            "thread_id": 11,
            "username": "igor_dev",
            "first_name": "Игорь",
            "text": "у меня на проде это падало на миграции",
            "engaged": False,
            "reason": "reaction_not_a_question",
        }
        resp = self.client.post("/observe", json=body, headers=self.auth)
        self.assertEqual(resp.status_code, 200, resp.text)
        self.assertTrue(resp.json()["ok"])
        self.assertEqual(len(self.seen), 1)
        self.assertEqual(self.seen[0]["text"], body["text"])
        self.assertFalse(self.seen[0]["engaged"])

    def test_the_intake_is_behind_the_same_bearer_gate_as_mcp(self):
        resp = self.client.post("/observe", json={"message_id": 1})
        self.assertEqual(resp.status_code, 401)
        self.assertEqual(self.seen, [])

    def test_a_message_without_an_id_is_refused_not_stored(self):
        resp = self.client.post("/observe", json={"text": "без id"}, headers=self.auth)
        self.assertEqual(resp.status_code, 400)
        self.assertEqual(self.seen, [])

    def test_a_storage_failure_never_looks_like_success(self):
        async def boom(observation):
            raise RuntimeError("db down")

        storage.record_comment = boom
        resp = self.client.post("/observe", json={"message_id": 5}, headers=self.auth)
        self.assertEqual(resp.status_code, 500)
        self.assertFalse(resp.json()["ok"])


class TestChatSearchRanking(unittest.TestCase):
    COMMENTS = [
        {"text": "у меня argo не видел resources в диффе, лечится sync-опцией", "said_on": "2026-09-01"},
        {"text": "argo argo argo и ещё раз argo, всё про argo", "said_on": "2026-08-01"},
        {"text": "+", "said_on": "2026-09-05"},
        {"text": "это пост канала про argo", "said_on": "2026-09-07", "is_channel_post": True},
        {"text": "вообще не про то, тут только про кофе и погоду", "said_on": "2026-09-06"},
    ]

    def test_more_distinct_words_matched_wins(self):
        got = search_sql.search_comments(self.COMMENTS, "argo resources")
        self.assertTrue(got)
        self.assertIn("resources", got[0]["text"])

    def test_repeating_a_word_does_not_buy_rank(self):
        """The SQL counts query words present, not occurrences; the twin must agree."""
        got = search_sql.search_comments(self.COMMENTS, "argo")
        self.assertEqual(got[0]["said_on"], "2026-09-01")

    def test_channel_posts_and_reactions_are_not_chat_memory(self):
        got = search_sql.search_comments(self.COMMENTS, "argo")
        for comment in got:
            self.assertFalse(comment.get("is_channel_post"))
            self.assertGreaterEqual(len(comment["text"]), 12)

    def test_no_overlap_returns_nothing_rather_than_the_newest(self):
        self.assertEqual(search_sql.search_comments(self.COMMENTS, "квантовая криптография"), [])


if __name__ == "__main__":
    unittest.main()
