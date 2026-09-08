"""Skill-based prompt loading: a thin core plus domain files fetched on demand.

Platform track 2 (архитектурные подходы). The layout matches what
``backend/internal/agentruntime/domains.go`` already reads, so a repo laid out
this way works with the Go runtime's ``get_domain_instruction`` and with an
MCP ``load_skill`` tool without a second copy of the files:

    agents/<agent-name>/core.md
    agents/<agent-name>/domains/<domain>.md

Why not one big prompt: a monolith pays for every domain on every turn, and
the parts drift because nobody can see which paragraph is load-bearing. A
domain file is small enough to read in review and cheap enough to A/B.

Path safety is enforced here rather than in each caller. The domain name is
matched against a strict pattern, not sanitized by deletion, because deleting
".." from an attacker-chosen string is a known-weak defence.
"""

import os
import re

DOMAIN_RE = re.compile(r"^[a-z0-9][a-z0-9-]{0,39}$")
AGENT_RE = re.compile(r"^[a-z0-9][a-z0-9-]{0,63}$")


class SkillError(ValueError):
    pass


class SkillSet:
    """One agent's prompt tree rooted at ``base_path``."""

    def __init__(self, base_path: str, agent_name: str):
        if not AGENT_RE.fullmatch(agent_name):
            raise SkillError(f"invalid agent name: {agent_name!r}")
        self.base_path = base_path
        self.agent_name = agent_name

    @property
    def agent_dir(self) -> str:
        return os.path.join(self.base_path, "agents", self.agent_name)

    def core(self) -> str:
        path = os.path.join(self.agent_dir, "core.md")
        try:
            with open(path, encoding="utf-8") as fh:
                return fh.read()
        except FileNotFoundError as exc:
            raise SkillError(f"core prompt not found: {path}") from exc

    def domains(self) -> list[str]:
        d = os.path.join(self.agent_dir, "domains")
        if not os.path.isdir(d):
            return []
        return sorted(f[:-3] for f in os.listdir(d) if f.endswith(".md"))

    def load(self, domain: str) -> str:
        if not DOMAIN_RE.fullmatch(domain):
            raise SkillError(f"invalid domain name: {domain!r}")
        path = os.path.join(self.agent_dir, "domains", domain + ".md")
        try:
            with open(path, encoding="utf-8") as fh:
                return fh.read()
        except FileNotFoundError as exc:
            raise SkillError(f"domain not found: {domain}") from exc

    def audit(self) -> dict:
        """Cheap structural check usable as a pre-merge gate.

        Reports what a reviewer would otherwise have to notice by hand: a
        core prompt that has grown into a monolith, and domain files the core
        never names (dead prompt text nobody routes to).
        """
        core = self.core()
        names = self.domains()
        unreferenced = [n for n in names if n not in core]
        return {
            "agent": self.agent_name,
            "core_chars": len(core),
            "core_lines": core.count("\n") + 1,
            "domains": names,
            "unreferenced_domains": unreferenced,
        }
