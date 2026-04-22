"""Pipeline inspection service."""

from __future__ import annotations

from ..client import CircleCIClient
from ..models import CommandResponse
from ..project_resolver import ResolvedProject
from .status import build_pipeline_summary, summarize_pipeline


def run(*, client: CircleCIClient, project: ResolvedProject, pipeline_id: str) -> CommandResponse:
    summary = build_pipeline_summary(client, project, pipeline_id=pipeline_id)
    return summarize_pipeline(summary, command="pipeline", target=project.target)
