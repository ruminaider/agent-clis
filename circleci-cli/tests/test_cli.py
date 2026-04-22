import json

from circleci_cli.cli import main
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


def test_invalid_target_exits_two():
    try:
        main(["status", "--target", "invalid"])
    except SystemExit as exc:
        assert exc.code == 2
    else:
        raise AssertionError("expected argparse to exit with code 2")


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
