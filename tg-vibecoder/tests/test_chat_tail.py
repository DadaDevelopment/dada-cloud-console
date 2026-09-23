"""Contract tests for the chat_tail retrieval tool.

chat_tail is the dialogue-flow primitive: the last N lines of one chat in
order, so the agent can read what the room actually said before deciding to
speak. These tests pin the SQL contract the manifest runs and the shape the
model sees, without a database.
"""

import unittest

import search_sql


SAMPLE_ROWS = [
    {
        "author": "тема",
        "username": "tema",
        "text": "не ну опус",
        "said_at": "22.09 21:11",
        "is_post": False,
        "bot_answered": False,
    },
    {
        "author": "Канал",
        "username": "",
        "text": "пост про новые модели",
        "said_at": "22.09 21:05",
        "is_post": True,
        "bot_answered": False,
    },
    {
        "author": "вайткодер",
        "username": "nevergonnagiveup_bot",
        "text": "согласен, опус",
        "said_at": "22.09 21:12",
        "is_post": False,
        "bot_answered": True,
    },
]


class ChatTailContract(unittest.TestCase):
    def test_sql_scopes_to_one_chat_and_orders_newest_first(self):
        self.assertIn("c.chat_id = $1", search_sql.CHAT_TAIL_SQL)
        self.assertIn("ORDER BY c.sent_at DESC", search_sql.CHAT_TAIL_SQL)
        self.assertIn("LIMIT $2::int", search_sql.CHAT_TAIL_SQL)

    def test_sql_exposes_the_fields_the_prompt_promises(self):
        for column in ("c.author", "c.username", "c.text", "c.is_channel_post", "c.engaged"):
            self.assertIn(column, search_sql.CHAT_TAIL_SQL)

    def test_sql_does_not_hide_short_lines(self):
        self.assertNotIn("length(c.text) >= 12", search_sql.CHAT_TAIL_SQL)

    def test_manifest_registers_chat_tail(self):
        import manifests_seed

        names = [m["name"] for m in manifests_seed.DEFAULT_MANIFESTS]
        self.assertIn("chat_tail", names)
        manifest = next(m for m in manifests_seed.DEFAULT_MANIFESTS if m["name"] == "chat_tail")
        self.assertEqual(manifest["config"]["query"], search_sql.CHAT_TAIL_SQL)
        self.assertEqual(manifest["config"]["param_order"], ["chat_id", "limit"])

    def test_rows_read_like_a_dialogue_from_newest_to_oldest(self):
        texts = [row["text"] for row in SAMPLE_ROWS]
        self.assertEqual(texts[0], "не ну опус")
        self.assertTrue(SAMPLE_ROWS[-1]["bot_answered"])

    def test_short_reactions_survive_the_tail(self):
        tail = search_sql.CHAT_TAIL_SQL
        self.assertIn("length(c.text) > 0", tail)
        self.assertNotIn(">= 12", tail)


if __name__ == "__main__":
    unittest.main()
