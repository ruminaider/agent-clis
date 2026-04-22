"""Format normalized responses for stdout and stderr."""

from __future__ import annotations

import json

from .errors import CliError
from .models import CommandResponse


def format_success(response: CommandResponse, output_format: str) -> str:
    payload = response.to_dict()
    if output_format == "text":
        target = f" [{response.target}]" if response.target else ""
        branch = payload.get("data", {}).get("pipeline", {}).get("branch")
        branch_suffix = f" on {branch}" if branch else ""
        return f"{response.command}{target}{branch_suffix}: {response.summary}"
    return json.dumps(payload, indent=2)


def format_error(error: CliError) -> str:
    return json.dumps(
        {
            "error": {
                "code": error.code,
                "message": error.message,
                "details": error.details,
            }
        },
        indent=2,
    )
