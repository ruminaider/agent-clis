import json
from types import SimpleNamespace

import pytest

from circleci_cli.cli import main
from circleci_cli.errors import AuthError, ConfigError
from circleci_cli.models import CommandResponse
from circleci_cli.project_resolver import ResolvedProject, TargetConfig


class _FakeClient:
    def __init__(self, token: str):
        self.token = token

    def get_me(self):
        return {"login": "albert"}


class _ExplodingClient:
    def __init__(self, token: str):
        self.token = token

    def get_me(self):
        raise AssertionError("should not be called")


class _InvalidTokenClient:
    def __init__(self, token: str):
        self.token = token

    def get_me(self):
        raise AuthError("Unauthorized", details={"status": 401})


class _MixedStatusClient:
    def __init__(self, token: str):
        self.token = token

    def list_pipelines(self, project_slug, branch=None):
        return [
            {
                "id": "pipeline-old",
                "number": 100,
                "state": "created",
                "created_at": "2026-04-21T00:00:00Z",
                "vcs": {"branch": branch or "main", "revision": "abc123"},
            },
            {
                "id": "pipeline-new",
                "number": 101,
                "state": "created",
                "created_at": "2026-04-22T00:00:00Z",
                "vcs": {"branch": branch or "main", "revision": "abc123"},
            },
        ]

    def get_pipeline(self, pipeline_id):
        raise AssertionError("unexpected get_pipeline call")

    def list_workflows(self, pipeline_id):
        if pipeline_id == "pipeline-old":
            return [{"id": "workflow-old", "name": "build-and-deploy-tonic", "status": "success", "created_at": "2026-04-21T00:00:00Z", "stopped_at": "2026-04-21T00:00:00Z"}]
        return [{"id": "workflow-new", "name": "build-and-deploy-tonic", "status": "failed", "created_at": "2026-04-22T00:00:00Z", "stopped_at": "2026-04-22T00:00:00Z"}]

    def list_workflow_jobs(self, workflow_id):
        if workflow_id == "workflow-old":
            return [{"name": "tonic-tests", "status": "success", "job_number": 76, "type": "build", "started_at": "2026-04-21T00:00:00Z", "stopped_at": "2026-04-21T00:00:00Z"}]
        return [
            {"name": "tonic-tests", "status": "running", "job_number": 78, "type": "build", "started_at": "2026-04-22T01:00:00Z", "stopped_at": "2026-04-22T01:00:00Z"},
            {"name": "tonic-tests", "status": "failed", "job_number": 77, "type": "build", "started_at": "2026-04-22T00:00:00Z", "stopped_at": "2026-04-22T00:00:00Z"},
        ]


class _RunningOnlyClient(_MixedStatusClient):
    def list_workflow_jobs(self, workflow_id):
        if workflow_id == "workflow-old":
            return [{"name": "tonic-tests", "status": "success", "job_number": 76, "type": "build", "started_at": "2026-04-21T00:00:00Z", "stopped_at": "2026-04-21T00:00:00Z"}]
        return [
            {"name": "tonic-tests", "status": "running", "job_number": 78, "type": "build", "started_at": "2026-04-22T01:00:00Z", "stopped_at": "2026-04-22T01:00:00Z"},
            {"name": "tonic-tests", "status": "running", "job_number": 77, "type": "build", "started_at": "2026-04-22T00:00:00Z", "stopped_at": "2026-04-22T00:00:00Z"},
        ]


def _read_json(stderr: str) -> dict:
    return json.loads(stderr)


def _resolved_project() -> ResolvedProject:
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


def test_help_exits_zero(capsys):
    try:
        main(["--help"])
    except SystemExit as exc:
        assert exc.code == 0
    stdout = capsys.readouterr().out
    assert "circleci-cli" in stdout
    assert "flaky-tests" in stdout


def test_doctor_outputs_json(capsys, monkeypatch):
    monkeypatch.setenv("PI_CIRCLECI_PROJECT_SLUG", "gh/Recora-Health/recora-health-back-end")
    monkeypatch.setenv("CIRCLECI_TOKEN", "secret")
    monkeypatch.setattr("circleci_cli.cli.CircleCIClient", _FakeClient)
    code = main(["doctor"])
    assert code == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload["command"] == "doctor"
    assert payload["data"]["project_slug"] == "gh/Recora-Health/recora-health-back-end"
    assert payload["data"]["auth_valid"] is True
    assert payload["data"]["token_present"] is True


def test_doctor_handles_invalid_token(capsys, monkeypatch):
    monkeypatch.setenv("PI_CIRCLECI_PROJECT_SLUG", "gh/Recora-Health/recora-health-back-end")
    monkeypatch.setattr("circleci_cli.cli.resolve_token", lambda: "secret")
    monkeypatch.setattr("circleci_cli.cli.CircleCIClient", _InvalidTokenClient)
    code = main(["doctor"])
    assert code == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload["data"]["token_present"] is True
    assert payload["data"]["auth_valid"] is False
    assert payload["data"]["token_error"] == "Unauthorized"


def test_doctor_handles_cli_error_during_token_resolution(capsys, monkeypatch):
    monkeypatch.setenv("PI_CIRCLECI_PROJECT_SLUG", "gh/Recora-Health/recora-health-back-end")
    monkeypatch.setattr("circleci_cli.cli.resolve_token", lambda: (_ for _ in ()).throw(ConfigError("token cache corrupt")))
    code = main(["doctor"])
    assert code == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload["data"]["token_present"] is False
    assert payload["data"]["auth_valid"] is False
    assert payload["data"]["token_error"] == "token cache corrupt"


def test_doctor_handles_missing_token(capsys, monkeypatch):
    monkeypatch.setenv("PI_CIRCLECI_PROJECT_SLUG", "gh/Recora-Health/recora-health-back-end")
    monkeypatch.delenv("CIRCLECI_TOKEN", raising=False)
    monkeypatch.delenv("PI_CIRCLECI_TOKEN", raising=False)
    monkeypatch.setattr("circleci_cli.cli.CircleCIClient", _ExplodingClient)
    code = main(["doctor"])
    assert code == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload["data"]["auth_valid"] is False
    assert payload["data"]["token_present"] is False
    assert payload["data"]["token_error"] == "Missing CircleCI token"


@pytest.mark.parametrize(
    ("argv", "message_fragment"),
    [
        (["nonsense"], "invalid choice"),
        (["pipeline"], "the following arguments are required: pipeline_id"),
        (["status", "--target", "invalid"], "invalid choice"),
    ],
)
def test_usage_failures_return_json_and_usage_exit_code(argv, message_fragment, capsys):
    code = main(argv)
    assert code == 2
    payload = _read_json(capsys.readouterr().err)
    assert payload["error"]["code"] == "USAGE_ERROR"
    assert message_fragment in payload["error"]["message"]
    assert payload["error"]["details"]["usage"].startswith("usage:")


@pytest.mark.parametrize(
    ("argv", "message_fragment"),
    [
        (["logs", "--job-number", "12", "--pipeline-id", "pipe-1"], "--job-number is standalone"),
        (["logs", "--job-number", "12", "--workflow-id", "wf-1"], "--job-number is standalone"),
        (["logs", "--job-number", "12", "--job-name", "build"], "--job-number is standalone"),
        (["logs", "--workflow-id", "wf-1", "--pipeline-id", "pipe-1"], "mutually exclusive"),
        (["logs", "--job-name", "build"], "requires exactly one"),
        (["logs", "--job-name", "build", "--workflow-id", "wf-1", "--pipeline-id", "pipe-1"], "mutually exclusive"),
    ],
)
def test_logs_selector_validation_returns_json_and_stops_before_service_execution(argv, message_fragment, capsys, monkeypatch):
    monkeypatch.setattr("circleci_cli.cli.resolve_token", lambda: (_ for _ in ()).throw(AssertionError("should not resolve token")))
    monkeypatch.setattr("circleci_cli.cli.resolve_project", lambda *args, **kwargs: (_ for _ in ()).throw(AssertionError("should not resolve project")))

    code = main(argv)
    assert code == 2
    payload = _read_json(capsys.readouterr().err)
    assert payload["error"]["code"] == "USAGE_ERROR"
    assert message_fragment in payload["error"]["message"]


def test_logs_without_selectors_uses_default_resolution_path(capsys, monkeypatch):
    monkeypatch.setattr("circleci_cli.cli.resolve_token", lambda: "secret")
    monkeypatch.setattr(
        "circleci_cli.cli.resolve_project",
        lambda *args, **kwargs: SimpleNamespace(project_slug="gh/org/repo", target=None, branch=None, commit=None, targets={}),
    )
    monkeypatch.setattr(
        "circleci_cli.cli.logs.run",
        lambda **kwargs: CommandResponse(command="logs", target=None, summary="default path", data={"selector": "default"}),
    )

    code = main(["logs"])
    assert code == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload["command"] == "logs"
    assert payload["data"]["selector"] == "default"


def test_config_validate_failure_returns_delegated_exit_code_and_json_envelope(capsys, monkeypatch):
    monkeypatch.setattr("circleci_cli.cli.is_installed", lambda: True)
    monkeypatch.setattr(
        "circleci_cli.cli.validate_config",
        lambda path: SimpleNamespace(returncode=7, stdout="validation failed", stderr="config invalid"),
    )

    code = main(["config", "validate", "--path", ".circleci/config.yml"])
    assert code == 7
    payload = json.loads(capsys.readouterr().out)
    assert payload["command"] == "config validate"
    assert payload["summary"] == "Config validation failed"
    assert payload["data"] == {
        "path": ".circleci/config.yml",
        "stdout": "validation failed",
        "stderr": "config invalid",
        "returncode": 7,
    }
    assert payload["meta"]["delegated_to"] == "circleci"


def test_config_validate_success_returns_zero_and_json_envelope(capsys, monkeypatch):
    monkeypatch.setattr("circleci_cli.cli.is_installed", lambda: True)
    monkeypatch.setattr(
        "circleci_cli.cli.validate_config",
        lambda path: SimpleNamespace(returncode=0, stdout="config valid", stderr=""),
    )

    code = main(["config", "validate", "--path", ".circleci/config.yml"])
    assert code == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload["command"] == "config validate"
    assert payload["summary"] == "Config validation completed"
    assert payload["data"] == {
        "path": ".circleci/config.yml",
        "stdout": "config valid",
        "stderr": "",
        "returncode": 0,
    }
    assert payload["meta"]["delegated_to"] == "circleci"


def test_status_fail_on_ci_failure_returns_three(capsys, monkeypatch):
    monkeypatch.setenv("PI_CIRCLECI_PROJECT_SLUG", "gh/Recora-Health/recora-health-back-end")
    monkeypatch.setattr("circleci_cli.cli.resolve_token", lambda: "secret")
    monkeypatch.setattr("circleci_cli.cli.resolve_project", lambda *args, **kwargs: _resolved_project())
    monkeypatch.setattr("circleci_cli.cli.CircleCIClient", _MixedStatusClient)

    code = main(["status", "--target", "tonic", "--fail-on-ci-failure"])
    assert code == 3
    payload = json.loads(capsys.readouterr().out)
    assert payload["data"]["pipeline"]["status_counts"]["failed"] == 1
    assert payload["data"]["pipeline"]["primary_job"]["status"] == "failed"


def test_status_fail_on_ci_failure_returns_zero_for_running_only_pipeline(capsys, monkeypatch):
    monkeypatch.setenv("PI_CIRCLECI_PROJECT_SLUG", "gh/Recora-Health/recora-health-back-end")
    monkeypatch.setattr("circleci_cli.cli.resolve_token", lambda: "secret")
    monkeypatch.setattr("circleci_cli.cli.resolve_project", lambda *args, **kwargs: _resolved_project())
    monkeypatch.setattr("circleci_cli.cli.CircleCIClient", _RunningOnlyClient)

    code = main(["status", "--target", "tonic", "--fail-on-ci-failure"])
    assert code == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload["data"]["pipeline"]["status_counts"]["failed"] == 0
