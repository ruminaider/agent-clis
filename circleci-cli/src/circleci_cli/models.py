"""Normalized response models for circleci-cli."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass(slots=True)
class CommandResponse:
    command: str
    target: str | None
    summary: str
    data: dict[str, Any] = field(default_factory=dict)
    links: dict[str, Any] = field(default_factory=dict)
    meta: dict[str, Any] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        return {
            "command": self.command,
            "target": self.target,
            "summary": self.summary,
            "data": self.data,
            "links": self.links,
            "meta": self.meta,
        }
