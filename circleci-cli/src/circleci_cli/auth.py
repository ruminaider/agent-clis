"""Authentication helpers for circleci-cli."""

from __future__ import annotations

import os

from .errors import AuthError


def resolve_token() -> str:
    """Resolve the active CircleCI token from the environment. Comment by Claude."""
    token = os.getenv("CIRCLECI_TOKEN") or os.getenv("PI_CIRCLECI_TOKEN")
    if not token:
        raise AuthError("Missing CircleCI token", details={"env_vars": ["CIRCLECI_TOKEN", "PI_CIRCLECI_TOKEN"]})
    return token
