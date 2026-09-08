"""The intake that turns other people's comments into the agent's memory.

Nothing here imports ``server``: CI runs a bare apk ``python3`` with no
starlette, so a test that needed the framework could only be skipped, and the
skip is what let build #18 ship a red main. The route is a two-line adapter
over ``intake``; the decisions it delegates are all asserted below.
"""

import asyncio
import os
import sys
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)

sys.path.insert(0, os.path.join(os.path.dirname(ROOT), "agentkit"))

import intake
import search_sql
import transcript


class TestObserveIntake(unittest.TestCase):
    def setUp(self):
        self.seen = []

    async def record(self, observation):
        self.seen.append(observation)
        return "INSERT 0 1"

    def post(self, payload, record=None):
        async def read_json():
            if isinstance(payload, Exception):
                raise payload
            return payload

        return asyncio.run(intake.handle_observation(read_json, record or self.record))

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
        status, answer = self.post(body)
        self.assertEqual(status, 200)
        self.assertTrue(answer["ok"])
        self.assertEqual(len(self.seen), 1)
        self.assertEqual(self.seen[0]["text"], body["text"])
        self.assertFalse(self.seen[0]["engaged"])

    def test_a_message_without_an_id_is_refused_not_stored(self):
        status, answer = self.post({"text": "без id"})
        self.assertEqual(status, 400)
        self.assertEqual(answer["error"], "no message_id")
        self.assertEqual(self.seen, [])

    def test_a_body_that_is_not_json_is_refused_not_stored(self):
        status, _ = self.post(ValueError("not json"))
        self.assertEqual(status, 400)
        self.assertEqual(self.seen, [])

    def test_a_storage_failure_never_looks_like_success(self):
        async def boom(observation):
            raise RuntimeError("db down")

        status, answer = self.post({"message_id": 5}, record=boom)
        self.assertEqual(status, 500)
        self.assertFalse(answer["ok"])
        self.assertIn("db down", answer["error"])


class TestBearerGate(unittest.TestCase):
    """The intake sits behind the same token as /mcp, and every app here gets
    a public domain by default."""

    def test_the_configured_token_passes(self):
        self.assertTrue(intake.authorized({"authorization": "Bearer t"}, "Bearer t"))

    def test_a_missing_or_wrong_token_is_refused(self):
        self.assertFalse(intake.authorized({}, "Bearer t"))
        self.assertFalse(intake.authorized({"authorization": "Bearer other"}, "Bearer t"))
        self.assertFalse(intake.authorized({"authorization": "t"}, "Bearer t"))


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


class TestObservedMedia(unittest.TestCase):
    """A comment that was only a screenshot must not enter the corpus blank."""

    def test_gateway_media_fields_survive_intake(self):
        seen = []

        async def record(observation):
            seen.append(observation)
            return "INSERT 0 1"

        async def read_json():
            return {
                "chat_id": -1002171703932,
                "message_id": 77,
                "username": "igor_dev",
                "text": "",
                "media_kind": "image",
                "media_description": "скриншот терминала: ImportError cannot import name embed",
            }

        status, body = asyncio.run(intake.handle_observation(read_json, record))
        self.assertEqual((status, body["ok"]), (200, True))
        self.assertEqual(seen[0]["media_kind"], "image")

    def test_the_stored_line_clears_the_chat_search_floor(self):
        line = transcript.media_context("image", "скриншот терминала с ImportError")
        self.assertTrue(line.startswith("[изображение:"))
        self.assertGreaterEqual(len(line), 12)

    def test_an_unreadable_picture_still_leaves_a_searchable_line(self):
        line = transcript.media_context("image")
        self.assertEqual(line, "[изображение: описание недоступно]")

    def test_chat_search_reads_the_media_column(self):
        self.assertIn("c.media", search_sql.CHAT_SEARCH_SQL)
if __name__ == "__main__":
    unittest.main()
