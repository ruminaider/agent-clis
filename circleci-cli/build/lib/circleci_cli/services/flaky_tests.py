"""Flaky test analysis service."""

from __future__ import annotations

from typing import Any

from ..client import CircleCIClient
from ..models import CommandResponse
from ..project_resolver import ResolvedProject, TargetConfig


def _item_value(item: dict[str, Any], *keys: str) -> Any:
    for key in keys:
        if key in item and item.get(key) is not None:
            return item.get(key)
    return None


def _matches_target(item: dict[str, Any], target: TargetConfig | None) -> bool:
    if target is None:
        return True
    workflow_name = _item_value(item, "workflow-name", "workflow_name")
    job_name = _item_value(item, "job-name", "job_name")
    if target.workflows and workflow_name not in target.workflows:
        return False
    if target.jobs and job_name not in target.jobs:
        return False
    return True


def _to_int(value: Any) -> int:
    try:
        return int(value or 0)
    except (TypeError, ValueError):
        return 0


def _normalize_entry(item: dict[str, Any]) -> dict[str, Any]:
    times_flaked = _to_int(_item_value(item, "times-flaked", "times_flaked", "flake_count"))
    time_wasted = _to_int(_item_value(item, "time-wasted", "time_wasted", "time_wasted_seconds"))
    workflow_name = _item_value(item, "workflow-name", "workflow_name")
    job_name = _item_value(item, "job-name", "job_name")
    workflow_id = _item_value(item, "workflow-id", "workflow_id")
    pipeline_number = _item_value(item, "pipeline-number", "pipeline_number")
    return {
        "test_name": _item_value(item, "test-name", "test_name", "name"),
        "classname": item.get("classname"),
        "file": item.get("file"),
        "source": item.get("source"),
        "job_name": job_name,
        "job_number": _item_value(item, "job-number", "job_number"),
        "pipeline_number": pipeline_number,
        "workflow_id": workflow_id,
        "workflow_name": workflow_name,
        "workflow_created_at": _item_value(item, "workflow-created-at", "workflow_created_at"),
        "times_flaked": times_flaked,
        "time_wasted_seconds": time_wasted,
        "flake_score": (times_flaked * 1000) + time_wasted,
        "links": {
            "pipeline": f"https://app.circleci.com/pipelines/{pipeline_number}" if pipeline_number is not None else None,
            "workflow": f"https://app.circleci.com/pipelines/workflows/{workflow_id}" if workflow_id is not None else None,
        },
    }


def run(*, client: CircleCIClient, project: ResolvedProject, window: int, min_failures: int) -> CommandResponse:
    payload = client.get_flaky_tests(project.project_slug)
    target_config = project.targets.get(project.target or "all")
    entries = payload.get("flaky-tests") or []
    filtered = [_normalize_entry(entry) for entry in entries if _matches_target(entry, target_config)]
    ranked = sorted(filtered, key=lambda item: item["flake_score"], reverse=True)
    kept = [item for item in ranked if item["times_flaked"] >= min_failures][:window]

    test_noun = "test" if len(kept) == 1 else "tests"
    summary = f"Found {len(kept)} flaky {test_noun} for {project.target or 'all'}"
    top_test = kept[0] if kept else None
    return CommandResponse(
        command="flaky-tests",
        target=project.target,
        summary=summary,
        data={
            "project_slug": project.project_slug,
            "reported_total": payload.get("total-flaky-tests"),
            "matched_target": len(filtered),
            "returned": len(kept),
            "window": window,
            "min_failures": min_failures,
            "branch_filter_supported": False,
            "top_test": top_test,
            "tests": kept,
        },
        meta={
            "notes": [
                "CircleCI flaky test insights are project-level and not branch-scoped.",
                "flake_score weights times_flaked first, then time_wasted_seconds.",
            ]
        },
    )
