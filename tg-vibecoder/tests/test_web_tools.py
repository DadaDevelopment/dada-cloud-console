"""web_tools tests: pure parts run offline, the HTTP paths are stubbed."""

import os
import sys
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

try:
    import httpx
except ImportError:
    httpx = None

import web_tools

_SKIP_REASON = "httpx not installed (CI python lane ships stdlib only)"


class StripHtmlTest(unittest.TestCase):
    def test_tags_and_entities(self):
        raw = "<td>&nbsp;<b>GPT-5.6</b> sets &amp; breaks</td>"
        got = " ".join(web_tools._strip_html(raw).split())
        self.assertEqual(got, "GPT-5.6 sets & breaks")


class ShortenTest(unittest.TestCase):
    def test_six_words(self):
        self.assertEqual(web_tools._shorten("a b c d e f g h"), "a b c d e f")


@unittest.skipIf(httpx is None, _SKIP_REASON)
class SearchWebTest(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self._until = web_tools._SEARCH_DOWN.get("until", 0.0)
        web_tools._SEARCH_DOWN["until"] = 0.0

    def tearDown(self):
        web_tools._SEARCH_DOWN["until"] = self._until

    @unittest.skipIf(httpx is None, _SKIP_REASON)
    async def test_cooldown_returns_empty_without_http(self):
        web_tools._SEARCH_DOWN["until"] = float("inf")
        results = await web_tools.search_web("anything")
        self.assertEqual(results, [])

    @unittest.skipIf(httpx is None, _SKIP_REASON)
    async def test_first_result_wins_and_clears_cooldown(self):
        page = (
            '<a href="/l/?uddg=https%3A%2F%2Fexample.com%2Fa">Title A</a>'
            '<td class="result-snippet">snippet one</td>'
            '<a href="/l/?uddg=https%3A%2F%2Fexample.com%2Fb">Title B</a>'
            '<td class="result-snippet">snippet two</td>'
        )

        class FakeResp:
            text = page

            def raise_for_status(self):
                return None

        class FakeClient:
            def __init__(self, *a, **k):
                pass

            async def __aenter__(self):
                return self

            async def __aexit__(self, *exc):
                return False

            async def get(self, url, params=None):
                return FakeResp()

        with mock.patch.object(httpx, "AsyncClient", FakeClient):
            results = await web_tools.search_web("best model 2026 question")
        self.assertEqual(len(results), 2)
        self.assertEqual(results[0]["url"], "https://example.com/a")
        self.assertEqual(results[0]["title"], "Title A")
        self.assertEqual(results[0]["snippet"], "snippet one")
        self.assertEqual(web_tools._SEARCH_DOWN["until"], 0.0)

    @unittest.skipIf(httpx is None, _SKIP_REASON)
    async def test_engine_down_sets_cooldown(self):
        with mock.patch.object(httpx.AsyncClient, "get", side_effect=OSError("down")):
            results = await web_tools.search_web("best model 2026 question")
        self.assertEqual(results, [])
        self.assertGreater(web_tools._SEARCH_DOWN["until"], 0.0)


@unittest.skipIf(httpx is None, _SKIP_REASON)
class FetchPageTextTest(unittest.IsolatedAsyncioTestCase):
    async def test_rejects_non_http(self):
        text = await web_tools.fetch_page_text("ftp://example.com/x")
        self.assertIn("http", text)

    @unittest.skipIf(httpx is None, _SKIP_REASON)
    async def test_html_is_stripped_and_capped(self):
        page = "<html><body><p>" + ("слово " * 5000) + "</p></body></html>"

        class FakeResp:
            text = page
            headers = {"content-type": "text/html; charset=utf-8"}

            def raise_for_status(self):
                return None

        class FakeClient:
            def __init__(self, *a, **k):
                pass

            async def __aenter__(self):
                return self

            async def __aexit__(self, *exc):
                return False

            async def get(self, url):
                return FakeResp()

        with mock.patch.object(httpx, "AsyncClient", FakeClient):
            text = await web_tools.fetch_page_text("https://example.com/big")
        self.assertLessEqual(len(text), web_tools._FETCH_LIMIT)

    @unittest.skipIf(httpx is None, _SKIP_REASON)
    async def test_never_raises(self):
        with mock.patch.object(httpx.AsyncClient, "get", side_effect=OSError("x")):
            text = await web_tools.fetch_page_text("https://example.com/dead")
        self.assertIn("Не удалось открыть", text)


if __name__ == "__main__":
    unittest.main()
