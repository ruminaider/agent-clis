"""Status and pipeline summary helpers."""

from __future__ import annotations

from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from typing import Any

from ..client import CircleCIClient
from ..errors import NotFoundError
from ..models import CommandResponse
from ..project_resolver import ResolvedProject, TargetConfig


_STATUS_MAP = {
    "success": "success",
    "fixed": "success",
    "no_tests": "success",
    "failed": "failed",
    "error": "failed",
    "failing": "failed",
    "timedout": "failed",
    "infrastructure_fail": "failed",
    "terminated-unknown": "failed",
    "running": "running",
    "queued": "running",
    "scheduled": "running",
    "created": "running",
    "on_hold": "blocked",
    "blocked": "blocked",
    "not_running": "blocked",
    "canceled": "canceled",
    "not_run": "not_run",
    "unauthorized": "failed",
}


@dataclass(slots=True)
class JobSummary:
    name: str
    status: str
    job_number: int | None
    type: str | None
    started_at: str | None
    stopped_at: str | None
    web_url: str | None


@dataclass(slots=True)
class WorkflowSummary:
    id: str
    name: str
    status: str
    created_at: str | None
    stopped_at: str | None
    jobs: list[JobSummary]


@dataclass(slots=True)
class PipelineSummary:
    id: str
    number: int | None
    state: str | None
    branch: str | None
    revision: str | None
    created_at: str | None
    project_slug: str
    workflows: list[WorkflowSummary]


def normalize_status(status: str | None) -> str:
    if not status:
        return "unknown"
    return _STATUS_MAP.get(status, status)


def _job_web_url(project_slug: str, job_number: int | None) -> str | None:
    if job_number is None:
        return None
    return f"https://circleci.com/{project_slug}/{job_number}"


def _job_summary(job: dict[str, Any], project_slug: str) -> JobSummary:
    job_number = job.get("job_number")
    return JobSummary(
        name=job.get("name") or "unknown",
        status=normalize_status(job.get("status")),
        job_number=job_number,
        type=job.get("type"),
        started_at=job.get("started_at"),
        stopped_at=job.get("stopped_at"),
        web_url=_job_web_url(project_slug, job_number),
    )


def _workflow_matches(workflow: dict[str, Any], target: TargetConfig | None) -> bool:
    if target is None or not target.workflows:
        return True
    return workflow.get("name") in target.workflows


def _job_matches(job: dict[str, Any], target: TargetConfig | None) -> bool:
    if target is None or not target.jobs:
        return True
    return job.get("name") in target.jobs


def _pipeline_matches(pipeline: dict[str, Any], commit: str | None) -> bool:
    if not commit:
        return True
    return pipeline.get("vcs", {}).get("revision") == commit


def _parse_timestamp(value: str | None) -> datetime:
    if not value:
        return datetime.min.replace(tzinfo=timezone.utc)
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return datetime.min.replace(tzinfo=timezone.utc)


def _job_priority(job: JobSummary) -> int:
    return {
        "failed": 4,
        "running": 3,
        "blocked": 2,
        "canceled": 1,
        "not_run": 1,
        "success": 0,
        "unknown": -1,
    }.get(job.status, -1)


def _workflow_priority(workflow: WorkflowSummary) -> int:
    return max((_job_priority(job) for job in workflow.jobs), default=-1)


def _job_sort_key(job: JobSummary) -> tuple[datetime, int, int, str]:
    return (
        _parse_timestamp(job.started_at or job.stopped_at),
        _job_priority(job),
        job.job_number or -1,
        job.name,
    )


def _workflow_sort_key(workflow: WorkflowSummary) -> tuple[datetime, int, str]:
    return (
        _parse_timestamp(workflow.created_at),
        _workflow_priority(workflow),
        workflow.id,
    )


def _pipeline_sort_key(candidate: dict[str, Any], workflows: list[WorkflowSummary]) -> tuple[datetime, int, int, str]:
    return (
        _parse_timestamp(candidate.get("created_at")),
        _workflow_priority(workflows[0]) if workflows else -1,
        candidate.get("number") or -1,
        candidate.get("id") or "",
    )


def build_pipeline_summary(client: CircleCIClient, project: ResolvedProject, *, pipeline_id: str | None = None) -> PipelineSummary:
    target_config = project.targets.get(project.target or "all")

    candidate_pipelines: list[dict[str, Any]]
    if pipeline_id:
        candidate_pipelines = [client.get_pipeline(pipeline_id)]
    else:
        candidate_pipelines = [item for item in client.list_pipelines(project.project_slug, branch=project.branch) if _pipeline_matches(item, project.commit)]
        if not candidate_pipelines:
            raise NotFoundError(
                "No matching pipelines found",
                details={"project_slug": project.project_slug, "branch": project.branch, "commit": project.commit},
            )

    ranked_candidates: list[tuple[tuple[datetime, int, int, str], dict[str, Any], list[WorkflowSummary]]] = []

    for candidate in candidate_pipelines:
        workflows = client.list_workflows(candidate["id"])
        candidate_summaries: list[WorkflowSummary] = []
        for workflow in workflows:
            if not _workflow_matches(workflow, target_config):
                continue
            jobs = client.list_workflow_jobs(workflow["id"])
            matching_jobs = sorted(
                (_job_summary(job, project.project_slug) for job in jobs if _job_matches(job, target_config)),
                key=_job_sort_key,
                reverse=True,
            )
            if not matching_jobs:
                continue
            candidate_summaries.append(
                WorkflowSummary(
                    id=workflow["id"],
                    name=workflow.get("name") or "unknown",
                    status=normalize_status(workflow.get("status")),
                    created_at=workflow.get("created_at"),
                    stopped_at=workflow.get("stopped_at"),
                    jobs=matching_jobs,
                )
            )
        if candidate_summaries:
            candidate_summaries.sort(key=_workflow_sort_key, reverse=True)
            ranked_candidates.append((_pipeline_sort_key(candidate, candidate_summaries), candidate, candidate_summaries))

    if not ranked_candidates:
        last_pipeline_id = candidate_pipelines[0]["id"] if candidate_pipelines else pipeline_id
        raise NotFoundError(
            "No matching workflows found for target",
            details={"target": project.target, "pipeline_id": last_pipeline_id},
        )

    _, pipeline, workflow_summaries = max(ranked_candidates, key=lambda item: item[0])

    return PipelineSummary(
        id=pipeline["id"],
        number=pipeline.get("number"),
        state=normalize_status(pipeline.get("state")),
        branch=pipeline.get("vcs", {}).get("branch"),
        revision=pipeline.get("vcs", {}).get("revision"),
        created_at=pipeline.get("created_at"),
        project_slug=project.project_slug,
        workflows=workflow_summaries,
    )


def _status_counts(summary: PipelineSummary) -> dict[str, int]:
    jobs = [job for workflow in summary.workflows for job in workflow.jobs]
    counts = {
        "success": 0,
        "failed": 0,
        "running": 0,
        "blocked": 0,
        "canceled": 0,
        "not_run": 0,
        "unknown": 0,
    }
    for job in jobs:
        counts[job.status] = counts.get(job.status, 0) + 1
    return counts


def _primary_job(summary: PipelineSummary) -> JobSummary | None:
    for workflow in summary.workflows:
        for job in workflow.jobs:
            return job
    return None


def _pipeline_links(summary: PipelineSummary) -> dict[str, Any]:
    links: dict[str, Any] = {}
    if summary.number is not None:
        links["pipeline"] = f"https://app.circleci.com/pipelines/{summary.project_slug}/{summary.number}"
    workflow_links = []
    for workflow in summary.workflows:
        workflow_links.append(
            {
                "id": workflow.id,
                "name": workflow.name,
                "url": f"https://app.circleci.com/pipelines/{summary.project_slug}/{summary.number}/workflows/{workflow.id}" if summary.number is not None else None,
            }
        )
    if workflow_links:
        links["workflows"] = workflow_links
    primary_job = _primary_job(summary)
    if primary_job is not None:
        links["primary_job"] = {
            "name": primary_job.name,
            "job_number": primary_job.job_number,
            "url": primary_job.web_url,
        }
    return links


def summarize_pipeline(summary: PipelineSummary, *, command: str, target: str | None) -> CommandResponse:
    primary_job = _primary_job(summary)
    if primary_job is not None and primary_job.status == "failed":
        headline = f"Pipeline {summary.number or summary.id} failed in {primary_job.name}"
    elif primary_job is not None and primary_job.status == "running":
        headline = f"Pipeline {summary.number or summary.id} is still running in {primary_job.name}"
    elif any(workflow.status == "running" for workflow in summary.workflows):
        headline = f"Pipeline {summary.number or summary.id} is still running"
    else:
        headline = f"Pipeline {summary.number or summary.id} is {summary.state or 'unknown'}"


    return CommandResponse(
        command=command,
        target=target,
        summary=headline,
        data={
            "pipeline": {
                "id": summary.id,
                "number": summary.number,
                "state": summary.state,
                "branch": summary.branch,
                "revision": summary.revision,
                "created_at": summary.created_at,
                "project_slug": summary.project_slug,
                "status_counts": _status_counts(summary),
                "primary_job": asdict(primary_job) if primary_job is not None else None,
                "workflows": [
                    {
                        "id": workflow.id,
                        "name": workflow.name,
                        "status": workflow.status,
                        "created_at": workflow.created_at,
                        "stopped_at": workflow.stopped_at,
                        "jobs": [asdict(job) for job in workflow.jobs],
                    }
                    for workflow in summary.workflows
                ],
            }
        },
        links=_pipeline_links(summary),
    )


def run(*, client: CircleCIClient, project: ResolvedProject) -> CommandResponse:
    summary = build_pipeline_summary(client, project)
    return summarize_pipeline(summary, command="status", target=project.target)
