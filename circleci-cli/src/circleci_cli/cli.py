"""Command line entrypoint for circleci-cli."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

from .auth import resolve_token
from .client import CircleCIClient
from .errors import AuthError, CliError, NotImplementedCliError, UsageError
from .formatters import format_error, format_success
from .models import CommandResponse
from .official_cli import is_installed, validate_config
from .project_resolver import resolve_project
from .services import flaky_tests, logs, pipeline, status


class StructuredArgumentParser(argparse.ArgumentParser):
    def error(self, message: str) -> None:
        raise UsageError(message, details={"usage": self.format_usage().strip()})

    def exit(self, status: int = 0, message: str | None = None) -> None:
        if status == 0:
            return super().exit(status, message)
        raise UsageError((message or "Invalid CLI usage").strip(), details={"usage": self.format_usage().strip()})


def _add_common_flags(parser: argparse.ArgumentParser, *, include_target: bool = True) -> None:
    parser.add_argument("--format", choices=("json", "text"), default="json", help="Output format")
    parser.add_argument("--project-slug", help="Explicit CircleCI project slug")
    if include_target:
        parser.add_argument("--target", choices=("auth", "care", "tonic", "agent-core", "all"), default="all")
    parser.add_argument("--branch", help="Git branch to inspect")
    parser.add_argument("--commit", help="Commit SHA to inspect")


def _make_parser() -> argparse.ArgumentParser:
    parser = StructuredArgumentParser(
        prog="circleci-cli",
        description="JSON-first CircleCI CLI for AI agents.",
    )
    subparsers = parser.add_subparsers(dest="command", required=True, parser_class=StructuredArgumentParser)

    doctor = subparsers.add_parser("doctor", help="Validate local CircleCI CLI prerequisites")
    doctor.add_argument("--format", choices=("json", "text"), default="json", help="Output format")
    doctor.add_argument("--project-slug", help="Explicit CircleCI project slug")

    status_parser = subparsers.add_parser("status", help="Inspect the latest relevant pipeline")
    _add_common_flags(status_parser)
    status_parser.add_argument(
        "--fail-on-ci-failure",
        action="store_true",
        help="Exit 3 when the command succeeds but the resolved pipeline has failed jobs",
    )

    pipeline_parser = subparsers.add_parser("pipeline", help="Inspect a pipeline by id")
    pipeline_parser.add_argument("pipeline_id", help="CircleCI pipeline id")
    _add_common_flags(pipeline_parser)

    logs_parser = subparsers.add_parser(
        "logs",
        help="Inspect failure logs",
        description="Inspect failure logs and resolve a job before fetching log excerpts.",
        epilog=(
            "Selector rules:\n"
            "  --job-number is standalone and cannot be combined with --pipeline-id, --workflow-id, or --job-name.\n"
            "  --workflow-id and --pipeline-id are mutually exclusive.\n"
            "  --job-name requires exactly one of --workflow-id or --pipeline-id.\n"
            "  No selector flags remains a valid default resolution path."
        ),
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    _add_common_flags(logs_parser)
    logs_parser.add_argument("--pipeline-id", help="CircleCI pipeline id, mutually exclusive with --workflow-id")
    logs_parser.add_argument("--workflow-id", help="CircleCI workflow id, mutually exclusive with --pipeline-id")
    logs_parser.add_argument("--job-number", type=int, help="CircleCI job number, standalone selector")
    logs_parser.add_argument("--job-name", help="CircleCI job name, requires exactly one of --workflow-id or --pipeline-id")
    logs_parser.add_argument("--failed-only", action="store_true", help="Restrict to failed steps")
    logs_parser.add_argument("--tail", type=int, default=200, help="Tail lines to return")

    flaky_parser = subparsers.add_parser("flaky-tests", help="Analyze flaky tests")
    _add_common_flags(flaky_parser)
    flaky_parser.add_argument("--window", type=int, default=20, help="Number of recent tests to return")
    flaky_parser.add_argument("--min-failures", type=int, default=2, help="Minimum flake count to report")

    config_parser = subparsers.add_parser("config", help="Delegate config validation to official CircleCI CLI")
    config_subparsers = config_parser.add_subparsers(dest="config_command", required=True)
    validate_parser = config_subparsers.add_parser("validate", help="Validate .circleci/config.yml")
    validate_parser.add_argument("--path", default=".circleci/config.yml", help="Path to config file")
    validate_parser.add_argument("--format", choices=("json", "text"), default="json", help="Output format")

    return parser


def _doctor(args: argparse.Namespace) -> CommandResponse:
    project = resolve_project(args.project_slug, target="all")
    token_present = False
    auth_valid = False
    actor_login = None
    token_error = None

    try:
        token = resolve_token()
        token_present = bool(token)
        try:
            me = CircleCIClient(token=token).get_me()
            auth_valid = True
            actor_login = me.get("login")
        except AuthError as error:
            token_error = error.message
        except CliError as error:
            token_error = error.message
    except CliError as error:
        token_error = error.message

    summary = "CircleCI auth is valid" if auth_valid else "CircleCI auth is not ready"
    return CommandResponse(
        command="doctor",
        target="all",
        summary=summary,
        data={
            "token_present": token_present,
            "auth_valid": auth_valid,
            "actor_login": actor_login,
            "project_slug": project.project_slug,
            "official_circleci_installed": is_installed(),
            "token_error": token_error,
        },
    )


def _config_validate(args: argparse.Namespace) -> CommandResponse:
    if not is_installed():
        raise NotImplementedCliError("config validate without official circleci binary")
    result = validate_config(Path(args.path))
    return CommandResponse(
        command="config validate",
        target=None,
        summary="Config validation completed" if result.returncode == 0 else "Config validation failed",
        data={"path": args.path, "stdout": result.stdout, "stderr": result.stderr, "returncode": result.returncode},
        meta={"delegated_to": "circleci"},
    )


def _validate_logs_selectors(args: argparse.Namespace) -> None:
    has_pipeline_id = args.pipeline_id is not None
    has_workflow_id = args.workflow_id is not None
    has_job_number = args.job_number is not None
    has_job_name = args.job_name is not None

    if has_job_number and (has_pipeline_id or has_workflow_id or has_job_name):
        raise UsageError(
            "--job-number is standalone and cannot be combined with --pipeline-id, --workflow-id, or --job-name.",
            details={
                "pipeline_id": args.pipeline_id,
                "workflow_id": args.workflow_id,
                "job_number": args.job_number,
                "job_name": args.job_name,
            },
        )

    if has_pipeline_id and has_workflow_id:
        raise UsageError(
            "--workflow-id and --pipeline-id are mutually exclusive.",
            details={"pipeline_id": args.pipeline_id, "workflow_id": args.workflow_id},
        )

    if has_job_name and (has_pipeline_id == has_workflow_id):
        raise UsageError(
            "--job-name requires exactly one of --workflow-id or --pipeline-id.",
            details={
                "pipeline_id": args.pipeline_id,
                "workflow_id": args.workflow_id,
                "job_name": args.job_name,
            },
        )


def main(argv: list[str] | None = None) -> int:
    parser = _make_parser()

    try:
        args = parser.parse_args(argv)

        if args.command == "doctor":
            response = _doctor(args)
        elif args.command in {"status", "pipeline", "logs", "flaky-tests"}:
            if args.command == "logs":
                _validate_logs_selectors(args)

            token = resolve_token()
            client = CircleCIClient(token=token)
            project = resolve_project(args.project_slug, args.target, branch=args.branch, commit=args.commit)

            if args.command == "status":
                response = status.run(client=client, project=project)
            elif args.command == "pipeline":
                response = pipeline.run(client=client, project=project, pipeline_id=args.pipeline_id)
            elif args.command == "logs":
                response = logs.run(
                    client=client,
                    project=project,
                    pipeline_id=args.pipeline_id,
                    workflow_id=args.workflow_id,
                    job_number=args.job_number,
                    job_name=args.job_name,
                    failed_only=args.failed_only,
                    tail=args.tail,
                )
            else:
                response = flaky_tests.run(
                    client=client,
                    project=project,
                    window=args.window,
                    min_failures=args.min_failures,
                )
        elif args.command == "config" and args.config_command == "validate":
            response = _config_validate(args)
        else:
            raise NotImplementedCliError(args.command)

        print(format_success(response, getattr(args, "format", "json")))
        if args.command == "config" and args.config_command == "validate":
            return int(response.data.get("returncode", 0))
        if getattr(args, "fail_on_ci_failure", False):
            failed_jobs = response.data.get("pipeline", {}).get("status_counts", {}).get("failed", 0)
            if failed_jobs > 0:
                return 3
        return 0
    except CliError as error:
        print(format_error(error), file=sys.stderr)
        return error.exit_code


if __name__ == "__main__":
    sys.exit(main())
