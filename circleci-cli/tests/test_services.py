import pytest

from circleci_cli.errors import NotFoundError
from circleci_cli.project_resolver import ResolvedProject, TargetConfig
from circleci_cli.services import flaky_tests, logs, pipeline, status


class FakeClient:
    def list_pipelines(self, project_slug, branch=None):
        return [
            {
                "id": "pipeline-success",
                "number": 100,
                "state": "created",
                "created_at": "2026-04-21T00:00:00Z",
                "vcs": {"branch": branch or "main", "revision": "abc123"},
            },
            {
                "id": "pipeline-1",
                "number": 101,
                "state": "created",
                "created_at": "2026-04-22T00:00:00Z",
                "vcs": {"branch": branch or "main", "revision": "abc123"},
            },
        ]

    def get_pipeline(self, pipeline_id):
        number = 100 if pipeline_id == "pipeline-success" else 101
        created_at = "2026-04-21T00:00:00Z" if pipeline_id == "pipeline-success" else "2026-04-22T00:00:00Z"
        return {
            "id": pipeline_id,
            "number": number,
            "state": "created",
            "created_at": created_at,
            "vcs": {"branch": "main", "revision": "abc123"},
        }

    def list_workflows(self, pipeline_id):
        status = "success" if pipeline_id == "pipeline-success" else "failed"
        workflow_id = "workflow-success" if pipeline_id == "pipeline-success" else "workflow-1"
        return [{"id": workflow_id, "name": "build-and-deploy-tonic", "status": status, "created_at": "x", "stopped_at": "y"}]

    def list_workflow_jobs(self, workflow_id):
        if workflow_id == "workflow-success":
            return [{"name": "tonic-tests", "status": "success", "job_number": 76, "type": "build", "started_at": "x", "stopped_at": "y"}]
        return [{"name": "tonic-tests", "status": "failed", "job_number": 77, "type": "build", "started_at": "x", "stopped_at": "y"}]

    def get_job_details(self, project_slug, job_number):
        return {
            "name": "tonic-tests",
            "status": "failed",
            "web_url": "https://example.test/job/77",
            "pipeline": {"id": "pipeline-1"},
            "latest_workflow": {"id": "workflow-1"},
        }

    def get_v1_job_build(self, vcs_type, org, repo, build_num):
        return {
            "steps": [
                {
                    "name": "RSpec",
                    "actions": [
                        {
                            "name": "Run tests",
                            "status": "failed",
                            "output_url": "https://example.test/output",
                        }
                    ],
                }
            ]
        }

    def get_text_url(self, url):
        return '[{"message": "line one\\nline two"}]'

    def get_job_tests(self, project_slug, job_number):
        return [{"name": "spec/example_spec.rb", "result": "failure", "message": "Expected true got false", "file": "spec/example_spec.rb"}]

    def get_job_artifacts(self, project_slug, job_number):
        return [{"path": "tmp/test-results.xml", "url": "https://example.test/artifact"}]

    def get_flaky_tests(self, project_slug):
        return {
            "total-flaky-tests": 2,
            "flaky-tests": [
                {
                    "test-name": "spec A",
                    "workflow-name": "build-and-deploy-tonic",
                    "job-name": "tonic-tests",
                    "times-flaked": 3,
                    "time-wasted": 10,
                    "workflow-id": "workflow-1",
                    "pipeline-number": 101,
                },
                {
                    "test-name": "spec B",
                    "workflow-name": "build-and-deploy-care",
                    "job-name": "care-tests",
                    "times-flaked": 1,
                    "time-wasted": 5,
                },
            ],
        }


def resolved_project() -> ResolvedProject:
    return ResolvedProject(
        project_slug="gh/Recora-Health/recora-health-back-end",
        target="tonic",
        targets={
            "tonic": TargetConfig(
                name="tonic",
                workflows=["build-and-deploy-tonic"],
                jobs=["tonic-tests"],
            )
        },
        branch="main",
        commit="abc123",
    )


class PipelineSelectionClient:
    def __init__(self, pipelines, workflows_by_pipeline, jobs_by_workflow):
        self._pipelines = pipelines
        self._workflows_by_pipeline = workflows_by_pipeline
        self._jobs_by_workflow = jobs_by_workflow

    def list_pipelines(self, project_slug, branch=None):
        return self._pipelines

    def get_pipeline(self, pipeline_id):
        for pipeline in self._pipelines:
            if pipeline["id"] == pipeline_id:
                return pipeline
        raise KeyError(pipeline_id)

    def list_workflows(self, pipeline_id):
        return self._workflows_by_pipeline[pipeline_id]

    def list_workflow_jobs(self, workflow_id):
        return self._jobs_by_workflow[workflow_id]


def _pipeline(pipeline_id, number, created_at):
    return {
        "id": pipeline_id,
        "number": number,
        "state": "created",
        "created_at": created_at,
        "vcs": {"branch": "main", "revision": "abc123"},
    }


def _workflow(workflow_id, status, created_at):
    return {
        "id": workflow_id,
        "name": "build-and-deploy-tonic",
        "status": status,
        "created_at": created_at,
        "stopped_at": created_at,
    }


def _job(job_number, status, started_at):
    return {
        "name": "tonic-tests",
        "status": status,
        "job_number": job_number,
        "type": "build",
        "started_at": started_at,
        "stopped_at": started_at,
    }


def test_status_response():
    response = status.run(client=FakeClient(), project=resolved_project())
    assert response.command == "status"
    assert response.target == "tonic"
    assert "failed" in response.summary
    assert response.data["pipeline"]["id"] == "pipeline-1"
    assert response.data["pipeline"]["primary_job"]["name"] == "tonic-tests"
    assert response.links["primary_job"]["job_number"] == 77


def test_latest_pipeline_wins_over_older_failed_pipeline():
    client = PipelineSelectionClient(
        pipelines=[
            _pipeline("pipeline-old", 100, "2026-04-21T00:00:00Z"),
            _pipeline("pipeline-new", 101, "2026-04-22T00:00:00Z"),
        ],
        workflows_by_pipeline={
            "pipeline-old": [_workflow("workflow-old", "failed", "2026-04-21T00:00:00Z")],
            "pipeline-new": [_workflow("workflow-new", "running", "2026-04-22T00:00:00Z")],
        },
        jobs_by_workflow={
            "workflow-old": [_job(76, "failed", "2026-04-21T00:00:00Z")],
            "workflow-new": [_job(77, "running", "2026-04-22T00:00:00Z")],
        },
    )

    response = status.run(client=client, project=resolved_project())
    assert response.data["pipeline"]["id"] == "pipeline-new"
    assert response.data["pipeline"]["state"] == "running"
    assert response.data["pipeline"]["primary_job"]["status"] == "running"
    assert response.links["primary_job"]["job_number"] == 77


def test_same_timestamp_pipelines_use_failure_aware_tiebreaker_deterministically():
    client = PipelineSelectionClient(
        pipelines=[
            _pipeline("pipeline-running", 200, "2026-04-22T00:00:00Z"),
            _pipeline("pipeline-failed", 201, "2026-04-22T00:00:00Z"),
        ],
        workflows_by_pipeline={
            "pipeline-running": [_workflow("workflow-running", "running", "2026-04-22T00:00:00Z")],
            "pipeline-failed": [_workflow("workflow-failed", "failed", "2026-04-22T00:00:00Z")],
        },
        jobs_by_workflow={
            "workflow-running": [_job(201, "running", "2026-04-22T00:00:00Z")],
            "workflow-failed": [_job(202, "failed", "2026-04-22T00:00:00Z")],
        },
    )

    first = status.run(client=client, project=resolved_project())
    second = status.run(client=client, project=resolved_project())
    assert first.data["pipeline"]["id"] == "pipeline-failed"
    assert second.data["pipeline"]["id"] == "pipeline-failed"
    assert first.data["pipeline"]["primary_job"]["status"] == "failed"
    assert first.links["primary_job"]["job_number"] == 202


def test_pipelines_returned_out_of_order_still_select_by_created_at():
    client = PipelineSelectionClient(
        pipelines=[
            _pipeline("pipeline-newest", 302, "2026-04-23T00:00:00Z"),
            _pipeline("pipeline-oldest", 300, "2026-04-21T00:00:00Z"),
            _pipeline("pipeline-middle", 301, "2026-04-22T00:00:00Z"),
        ],
        workflows_by_pipeline={
            "pipeline-newest": [_workflow("workflow-newest", "running", "2026-04-23T00:00:00Z")],
            "pipeline-oldest": [_workflow("workflow-oldest", "failed", "2026-04-21T00:00:00Z")],
            "pipeline-middle": [_workflow("workflow-middle", "failed", "2026-04-22T00:00:00Z")],
        },
        jobs_by_workflow={
            "workflow-newest": [_job(302, "running", "2026-04-23T00:00:00Z")],
            "workflow-oldest": [_job(300, "failed", "2026-04-21T00:00:00Z")],
            "workflow-middle": [_job(301, "failed", "2026-04-22T00:00:00Z")],
        },
    )

    response = status.run(client=client, project=resolved_project())
    assert response.data["pipeline"]["id"] == "pipeline-newest"
    assert response.links["pipeline"].endswith("/302")
    assert response.data["pipeline"]["primary_job"]["status"] == "running"


@pytest.mark.parametrize(
    ("raw_status", "expected"),
    [
        ("success", "success"),
        ("fixed", "success"),
        ("no_tests", "success"),
        ("failed", "failed"),
        ("error", "failed"),
        ("failing", "failed"),
        ("timedout", "failed"),
        ("infrastructure_fail", "failed"),
        ("terminated-unknown", "failed"),
        ("running", "running"),
        ("queued", "running"),
        ("scheduled", "running"),
        ("created", "running"),
        ("on_hold", "blocked"),
        ("blocked", "blocked"),
        ("not_running", "blocked"),
        ("canceled", "canceled"),
        ("not_run", "not_run"),
        ("unauthorized", "failed"),
    ],
)
def test_known_circleci_statuses_are_normalized(raw_status, expected):
    assert status.normalize_status(raw_status) == expected


def test_pipeline_response():
    response = pipeline.run(client=FakeClient(), project=resolved_project(), pipeline_id="pipeline-1")
    assert response.data["pipeline"]["id"] == "pipeline-1"
    assert response.data["pipeline"]["status_counts"]["failed"] == 1
    assert response.links["pipeline"].endswith("/101")


def test_logs_response():
    response = logs.run(
        client=FakeClient(),
        project=resolved_project(),
        pipeline_id=None,
        workflow_id=None,
        job_number=None,
        job_name=None,
        failed_only=True,
        tail=50,
    )
    assert response.data["job"]["job_number"] == 77
    assert response.data["failed_steps"][0]["excerpt"] == ["line one", "line two"]
    assert response.data["tests"]["total"] == 1
    assert response.data["artifacts"]["total"] == 1
    assert response.data["log_focus"]["failed_actions"] == 1
    assert response.data["root_cause"]["type"] == "failing_test"
    assert response.data["step_summary"][0].startswith("RSpec")


def test_flaky_tests_response():
    response = flaky_tests.run(client=FakeClient(), project=resolved_project(), window=10, min_failures=2)
    assert response.command == "flaky-tests"
    assert response.summary == "Found 1 flaky test for tonic"
    assert response.data["returned"] == 1
    assert response.data["matched_target"] == 1
    assert response.data["top_test"]["test_name"] == "spec A"
    assert response.data["tests"][0]["test_name"] == "spec A"
    assert response.data["tests"][0]["flake_score"] == 3010
    assert response.data["tests"][0]["links"]["workflow"].endswith("workflow-1")


def test_logs_can_resolve_job_by_name():
    response = logs.run(
        client=FakeClient(),
        project=resolved_project(),
        pipeline_id=None,
        workflow_id=None,
        job_number=None,
        job_name="tonic-tests",
        failed_only=True,
        tail=50,
    )
    assert response.data["job"]["job_number"] == 77


def test_logs_unknown_job_name_raises_not_found():
    with pytest.raises(NotFoundError):
        logs.run(
            client=FakeClient(),
            project=resolved_project(),
            pipeline_id=None,
            workflow_id=None,
            job_number=None,
            job_name="missing-job",
            failed_only=True,
            tail=50,
        )
