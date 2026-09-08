"""Parsers must survive both feed dialects and a dead source."""

import os
import sys
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)

import channel_ingest
import news_ingest

RSS = """<?xml version="1.0"?>
<rss version="2.0"><channel>
<item><title>Model X ships</title><link>https://e.com/1</link>
<description>Faster and cheaper</description><pubDate>Mon, 08 Sep 2026 10:00:00 GMT</pubDate></item>
<item><title>Agents get memory</title><link>https://e.com/2</link>
<description>Long term recall</description><pubDate>Mon, 08 Sep 2026 09:00:00 GMT</pubDate></item>
</channel></rss>"""

ATOM = """<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
<entry><title>Release notes</title><link href="https://e.com/a"/>
<summary>Coding agent improvements</summary><updated>2026-09-08T10:00:00Z</updated></entry>
</feed>"""


class TestFeeds(unittest.TestCase):
    def test_rss(self):
        items = news_ingest.parse_feed(RSS, "example", ["модели"])
        self.assertEqual(len(items), 2)
        self.assertEqual(items[0]["title"], "Model X ships")
        self.assertEqual(items[0]["url"], "https://e.com/1")
        self.assertIn("модели", items[0]["tags"])

    def test_atom(self):
        items = news_ingest.parse_feed(ATOM, "example", [])
        self.assertEqual(len(items), 1)
        self.assertEqual(items[0]["url"], "https://e.com/a")

    def test_garbage_does_not_raise(self):
        self.assertEqual(news_ingest.parse_feed("not xml at all", "example", []), [])

    def test_fingerprints_are_stable_and_distinct(self):
        first = news_ingest.parse_feed(RSS, "example", [])
        second = news_ingest.parse_feed(RSS, "example", [])
        self.assertEqual(first[0]["fingerprint"], second[0]["fingerprint"])
        self.assertNotEqual(first[0]["fingerprint"], first[1]["fingerprint"])


HTML = """
<div class="tgme_widget_message" data-post="chan/12">
  <div class="tgme_widget_message_text">Первый пост про агентов<br/>вторая строка &amp; хвост</div>
  <time datetime="2026-09-07T12:00:00+00:00"></time>
</div>
<div class="tgme_widget_message" data-post="chan/13">
  <div class="tgme_widget_message_text">Второй пост</div>
  <time datetime="2026-09-08T12:00:00+00:00"></time>
</div>
"""


class TestChannel(unittest.TestCase):
    def test_parses_posts(self):
        posts = channel_ingest.parse_page(HTML)
        self.assertEqual([p["message_id"] for p in posts], [12, 13])

    def test_unescapes_and_breaks_lines(self):
        posts = channel_ingest.parse_page(HTML)
        self.assertIn("\n", posts[0]["text"])
        self.assertIn("&", posts[0]["text"])
        self.assertNotIn("&amp;", posts[0]["text"])

    def test_empty_page_yields_nothing(self):
        self.assertEqual(channel_ingest.parse_page("<html></html>"), [])


if __name__ == "__main__":
    unittest.main()
