"""Structured error types for circleci-cli."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass(slots=True)
class CliError(Exception):
    """Base structured CLI error, surfaced as JSON. Comment by Claude."""

    code: str
    message: str
    details: dict[str, Any] = field(default_factory=dict)
    exit_code: int = 1


class UsageError(CliError):
    def __init__(self, message: str, details: dict[str, Any] | None = None) -> None:
        super().__init__(code="USAGE_ERROR", message=message, details=details or {}, exit_code=2)


class AuthError(CliError):
    def __init__(self, message: str, details: dict[str, Any] | None = None) -> None:
        super().__init__(code="AUTH_ERROR", message=message, details=details or {})


class ConfigError(CliError):
    def __init__(self, message: str, details: dict[str, Any] | None = None) -> None:
        super().__init__(code="CONFIG_ERROR", message=message, details=details or {})


class HttpError(CliError):
    def __init__(self, message: str, details: dict[str, Any] | None = None) -> None:
        super().__init__(code="HTTP_ERROR", message=message, details=details or {})


class NotFoundError(CliError):
    def __init__(self, message: str, details: dict[str, Any] | None = None) -> None:
        super().__init__(code="NOT_FOUND", message=message, details=details or {})


class RateLimitError(CliError):
    def __init__(self, message: str, details: dict[str, Any] | None = None) -> None:
        super().__init__(code="RATE_LIMITED", message=message, details=details or {})


class NotImplementedCliError(CliError):
    def __init__(self, command: str) -> None:
        super().__init__(
            code="NOT_IMPLEMENTED",
            message=f"{command} is scaffolded but not implemented yet",
            details={"command": command},
        )
