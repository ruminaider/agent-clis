"""Failure log retrieval service."""

from __future__ import annotations

from typing import Any

from ..client import CircleCIClient, split_project_slug
from ..errors import NotFoundError
from ..models import CommandResponse
from ..project_resolver import ResolvedProject
from .status import build_pipeline_summary

_FAILURE_STATUSES = {"failed", "timedout", "canceled", "infrastructure_fail", "error"}
_INTERESTING_STEP_SCORES = {
    "run spec suite": 100,
    "rspec": 90,
    "pytest": 90,
    "run tests": 80,
    "test": 40,
    "failed": 20,
}


def _matching_jobs(jobs: list[dict[str, Any]], job_name: str) -> list[dict[str, Any]]:
    normalized = job_name.strip().lower()
    return [job for job in jobs if str(job.get("name") or "").strip().lower() == normalized]


def _select_job(
    client: CircleCIClient,
    project: ResolvedProject,
    *,
    pipeline_id: str | None,
    workflow_id: str | None,
    job_number: int | None,
    job_name: str | None,
) -> tuple[int, dict[str, Any], str | None]:
    if job_number is not None:
        details = client.get_job_details(project.project_slug, job_number)
        workflow = details.get("latest_workflow") or {}
        return job_number, details, workflow.get("id")

    if workflow_id is not None:
        jobs = client.list_workflow_jobs(workflow_id)
        if job_name:
            matches = _matching_jobs(jobs, job_name)
            if len(matches) > 1:
                raise NotFoundError(
                    "Job name is ambiguous within workflow",
                    details={"workflow_id": workflow_id, "job_name": job_name, "matches": [job.get("name") for job in matches]},
                )
            if len(matches) == 1:
                chosen = matches[0]
            else:
                raise NotFoundError(
                    "Job name not found in workflow",
                    details={"workflow_id": workflow_id, "job_name": job_name, "available_jobs": [job.get("name") for job in jobs]},
                )
        else:
            chosen = next((job for job in jobs if job.get("status") in {"failed", "error", "failing"}), None)
            if chosen is None:
                chosen = jobs[0] if jobs else None
        if chosen is None or chosen.get("job_number") is None:
            raise NotFoundError("No jobs found for workflow", details={"workflow_id": workflow_id})
        selected_job_number = int(chosen["job_number"])
        details = client.get_job_details(project.project_slug, selected_job_number)
        return selected_job_number, details, workflow_id

    pipeline_summary = build_pipeline_summary(client, project, pipeline_id=pipeline_id)
    if job_name:
        matches = [job for workflow in pipeline_summary.workflows for job in workflow.jobs if str(job.name).strip().lower() == job_name.strip().lower()]
        if len(matches) > 1:
            raise NotFoundError(
                "Job name is ambiguous within pipeline",
                details={"pipeline_id": pipeline_id, "job_name": job_name, "matches": [job.name for job in matches]},
            )
        if len(matches) == 1 and matches[0].job_number is not None:
            match = matches[0]
            details = client.get_job_details(project.project_slug, match.job_number)
            workflow_id_for_match = next(
                (workflow.id for workflow in pipeline_summary.workflows if any(job.job_number == match.job_number for job in workflow.jobs)),
                None,
            )
            return match.job_number, details, workflow_id_for_match
        raise NotFoundError(
            "Job name not found in pipeline",
            details={
                "pipeline_id": pipeline_id,
                "job_name": job_name,
                "available_jobs": [job.name for workflow in pipeline_summary.workflows for job in workflow.jobs],
            },
        )

    for workflow in pipeline_summary.workflows:
        for job in workflow.jobs:
            if job.status == "failed" and job.job_number is not None:
                details = client.get_job_details(project.project_slug, job.job_number)
                return job.job_number, details, workflow.id

    first_job = next((job for workflow in pipeline_summary.workflows for job in workflow.jobs if job.job_number is not None), None)
    if first_job is None or first_job.job_number is None:
        raise NotFoundError("No jobs found for pipeline", details={"pipeline_id": pipeline_id})
    details = client.get_job_details(project.project_slug, first_job.job_number)
    return first_job.job_number, details, None


def _truncate_lines(text: str, tail: int) -> list[str]:
    lines = text.splitlines()
    return lines[-tail:] if tail > 0 else lines


def _extract_log_messages(output_payload: Any) -> list[str]:
    if isinstance(output_payload, list):
        messages: list[str] = []
        for item in output_payload:
            if isinstance(item, dict) and item.get("message"):
                messages.extend(str(item["message"]).splitlines())
        return messages
    if isinstance(output_payload, dict) and output_payload.get("message"):
        return str(output_payload["message"]).splitlines()
    if isinstance(output_payload, str):
        return output_payload.splitlines()
    return []


def _fetch_action_output(client: CircleCIClient, output_url: str, tail: int) -> list[str]:
    raw = client.get_text_url(output_url)
    try:
        import json

        payload = json.loads(raw)
    except ValueError:
        return _truncate_lines(raw, tail)
    return _truncate_lines("\n".join(_extract_log_messages(payload)), tail)


def _normalize_action(step: dict[str, Any], action: dict[str, Any], excerpt: list[str]) -> dict[str, Any]:
    return {
        "step": step.get("name"),
        "action": action.get("name"),
        "status": action.get("status"),
        "start_time": action.get("start_time"),
        "end_time": action.get("end_time"),
        "output_url": action.get("output_url"),
        "excerpt": excerpt,
    }


def _interesting_score(step_name: str | None, action_name: str | None) -> int:
    haystack = " ".join(part.lower() for part in [step_name or "", action_name or ""])
    score = 0
    for marker, marker_score in _INTERESTING_STEP_SCORES.items():
        if marker in haystack:
            score = max(score, marker_score)
    return score


def _select_log_sections(build: dict[str, Any], *, client: CircleCIClient, failed_only: bool, tail: int) -> tuple[list[dict[str, Any]], dict[str, int]]:
    failed_sections: list[dict[str, Any]] = []
    interesting_sections: list[tuple[int, dict[str, Any]]] = []
    successful_sections: list[dict[str, Any]] = []
    total_actions = 0

    for step in build.get("steps") or []:
        actions = step.get("actions") or []
        for action in actions:
            total_actions += 1
            status = str(action.get("status") or "").lower()
            output_url = action.get("output_url")
            excerpt = _fetch_action_output(client, output_url, tail) if output_url else []
            normalized = _normalize_action(step, action, excerpt)

            if status in _FAILURE_STATUSES:
                failed_sections.append(normalized)
            else:
                interesting_score = _interesting_score(step.get("name"), action.get("name"))
                if interesting_score > 0:
                    interesting_sections.append((interesting_score, normalized))
                else:
                    successful_sections.append(normalized)

    if failed_only:
        selected = failed_sections
    elif failed_sections:
        selected = failed_sections
    elif interesting_sections:
        ranked_interesting = [section for _, section in sorted(interesting_sections, key=lambda item: item[0], reverse=True)]
        selected = ranked_interesting[:3]
    else:
        selected = successful_sections[-3:]

    counts = {
        "total_actions": total_actions,
        "failed_actions": len(failed_sections),
        "interesting_actions": len(interesting_sections),
        "returned_actions": len(selected),
    }
    return selected, counts


def _result_is_failure(result: str | None) -> bool:
    return str(result or "").lower() not in {"success", "passed", "pass", "skipped", "pending"}


def _failure_root_cause(selected_sections: list[dict[str, Any]], failing_tests: list[dict[str, Any]]) -> dict[str, Any] | None:
    if failing_tests:
        first = failing_tests[0]
        return {
            "type": "failing_test",
            "name": first.get("name"),
            "classname": first.get("classname"),
            "file": first.get("file"),
            "message": first.get("message"),
        }
    if selected_sections:
        first = selected_sections[0]
        return {
            "type": "failing_step" if str(first.get("status", "")).lower() in _FAILURE_STATUSES else "relevant_step",
            "step": first.get("step"),
            "action": first.get("action"),
            "status": first.get("status"),
            "excerpt": first.get("excerpt"),
        }
    return None


def _step_summary(selected_sections: list[dict[str, Any]]) -> list[str]:
    summaries: list[str] = []
    for section in selected_sections[:3]:
        step = section.get("step") or section.get("action") or "unknown"
        status = section.get("status") or "unknown"
        summaries.append(f"{step}: {status}")
    return summaries


def run(
    *,
    client: CircleCIClient,
    project: ResolvedProject,
    pipeline_id: str | None,
    workflow_id: str | None,
    job_number: int | None,
    job_name: str | None,
    failed_only: bool,
    tail: int,
) -> CommandResponse:
    selected_job_number, job_details, resolved_workflow_id = _select_job(
        client,
        project,
        pipeline_id=pipeline_id,
        workflow_id=workflow_id,
        job_number=job_number,
        job_name=job_name,
    )
    vcs_type, org, repo = split_project_slug(project.project_slug)
    build = client.get_v1_job_build(vcs_type, org, repo, selected_job_number)
    selected_sections, section_counts = _select_log_sections(build, client=client, failed_only=failed_only, tail=tail)

    tests: list[dict[str, Any]] = []
    try:
        tests = client.get_job_tests(project.project_slug, selected_job_number)
    except Exception:
        tests = []

    artifacts: list[dict[str, Any]] = []
    try:
        artifacts = client.get_job_artifacts(project.project_slug, selected_job_number)
    except Exception:
        artifacts = []

    failing_tests = [test for test in tests if _result_is_failure(test.get("result"))]
    test_sample = failing_tests[:25] if failing_tests else tests[:10]
    artifact_sample = artifacts[:10]

    root_cause = _failure_root_cause(selected_sections, failing_tests)

    if failing_tests:
        test_noun = "test" if len(failing_tests) == 1 else "tests"
        summary = f"Job {selected_job_number} has {len(failing_tests)} failing {test_noun}"
    elif section_counts["failed_actions"] > 0:
        summary = f"Found {section_counts['failed_actions']} failed log sections for job {selected_job_number}"
    elif section_counts["interesting_actions"] > 0:
        summary = f"No failed steps. Returning {section_counts['returned_actions']} relevant log sections for job {selected_job_number}"
    else:
        summary = f"No failed steps. Returning {section_counts['returned_actions']} recent log sections for job {selected_job_number}"

    return CommandResponse(
        command="logs",
        target=project.target,
        summary=summary,
        data={
            "job": {
                "job_number": selected_job_number,
                "name": job_details.get("name"),
                "status": job_details.get("status"),
                "web_url": job_details.get("web_url"),
                "workflow_id": resolved_workflow_id,
                "pipeline_id": job_details.get("pipeline", {}).get("id"),
            },
            "log_focus": section_counts,
            "failed_steps": selected_sections,
            "root_cause": root_cause,
            "step_summary": _step_summary(selected_sections),
            "tests": {
                "total": len(tests),
                "failing": len(failing_tests),
                "sample": test_sample,
            },
            "artifacts": {
                "total": len(artifacts),
                "sample": artifact_sample,
            },
        },
        links={
            "job": job_details.get("web_url"),
        },
    )
