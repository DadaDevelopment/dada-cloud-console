"""Feed catalogue for the freshness ingest.

Every entry is a candidate, not a verified endpoint. ``news_ingest`` reports
per-source ``ok``/``error`` on each run and a broken feed never fails the run
as a whole, so the catalogue can be edited without a code review round.

hnrss rejects a query that mixes a bare term with a quoted phrase: the exact
string ``LLM OR "AI agent"`` answers 502 while either half alone answers 200,
so multi-term feeds are kept split rather than combined.

``tags`` are attached to every item from that source. They are what
``news_search`` matches on when the user's wording does not appear in the
title, so keep them the words a person would actually type.
"""

FEEDS = [
    {"name": "openai", "url": "https://openai.com/news/rss.xml", "tags": ["openai", "gpt", "модели"]},
    {"name": "anthropic-hn", "url": "https://hnrss.org/newest?q=Anthropic+OR+Claude&points=50", "tags": ["anthropic", "claude", "модели"]},
    {"name": "google-ai", "url": "https://blog.google/technology/ai/rss/", "tags": ["google", "gemini", "модели"]},
    {"name": "simonwillison", "url": "https://simonwillison.net/atom/everything/", "tags": ["llm", "тулинг", "практика"]},
    {"name": "github-changelog", "url": "https://github.blog/changelog/feed/", "tags": ["github", "copilot", "тулинг"]},
    {"name": "vscode", "url": "https://code.visualstudio.com/feed.xml", "tags": ["vscode", "редактор", "тулинг"]},
    {"name": "vercel", "url": "https://vercel.com/atom", "tags": ["vercel", "деплой", "фронтенд"]},
    {"name": "arxiv-cs-ai", "url": "http://export.arxiv.org/rss/cs.AI", "tags": ["arxiv", "research", "llm"]},
    {"name": "hn-llm", "url": "https://hnrss.org/newest?q=LLM&points=100", "tags": ["hackernews", "llm"]},
    {"name": "hn-agents", "url": "https://hnrss.org/newest?q=%22AI+agent%22&points=100", "tags": ["hackernews", "агенты"]},
    {"name": "huggingface", "url": "https://huggingface.co/blog/feed.xml", "tags": ["huggingface", "модели", "опенсорс"]},
    {"name": "habr-ai", "url": "https://habr.com/ru/rss/hubs/artificial_intelligence/articles/?fl=ru", "tags": ["habr", "ии", "русский"]},
]

KEYWORD_TAGS = {
    "агенты": ["agent", "agents", "агент", "mcp", "tool use", "tool-use"],
    "кодинг": ["code", "coding", "copilot", "cursor", "claude code", "codex", "ide", "вайбкод"],
    "модели": ["gpt-", "claude ", "gemini", "llama", "qwen", "deepseek", "mistral", "grok"],
    "цена": ["pricing", "price", "cheaper", "free tier", "цена", "тариф"],
    "локально": ["ollama", "llama.cpp", "vllm", "on-device", "local model", "локально"],
    "оценка": ["benchmark", "eval", "swe-bench", "бенчмарк", "эвал"],
}
