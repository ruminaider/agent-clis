import json
from types import SimpleNamespace

import pytest

from circleci_cli.cli import main
from circleci_cli.errors import AuthError
from circleci_cli.models import CommandResponse


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


def _read_json(stderr: str) -> dict:
    return json.loads(stderr)


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


def test_status_fail_on_ci_failure_returns_three(monkeypatch):
    monkeypatch.setenv("PI_CIRCLECI_PROJECT_SLUG", "gh/Recora-Health/recora-health-back-end")
    monkeypatch.setenv("CIRCLECI_TOKEN", "secret")

    class _Client:
        def __init__(self, token: str):
            self.token = token

    monkeypatch.setattr("circleci_cli.cli.CircleCIClient", _Client)
    monkeypatch.setattr(
        "circleci_cli.cli.status.run",
        lambda client, project: CommandResponse(
            command="status",
            target=project.target,
            summary="Pipeline 1 failed in tonic-tests",
            data={"pipeline": {"primary_job": {"status": "failed"}}},
        ),
    )
    code = main(["status", "--target", "tonic", "--fail-on-ci-failure"])
    assert code == 3
