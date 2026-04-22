"""Resolve project slug and target mappings for the current repo."""

from __future__ import annotations

import os
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml

from .errors import ConfigError


@dataclass(slots=True)
class TargetConfig:
    name: str
    workflows: list[str]
    jobs: list[str]


@dataclass(slots=True)
class ResolvedProject:
    project_slug: str
    target: str | None
    targets: dict[str, TargetConfig]
    branch: str | None
    commit: str | None


def _targets_file() -> Path:
    return Path(__file__).with_name("targets.yml")


def load_targets() -> dict[str, Any]:
    path = _targets_file()
    if not path.exists():
        raise ConfigError("targets.yml is missing", details={"path": str(path)})
    return yaml.safe_load(path.read_text()) or {}


def _git_output(args: list[str]) -> str | None:
    try:
        result = subprocess.run(args, capture_output=True, text=True, check=True)
    except (subprocess.CalledProcessError, FileNotFoundError):
        return None
    value = result.stdout.strip()
    return value or None


def current_git_branch() -> str | None:
    branch = _git_output(["git", "rev-parse", "--abbrev-ref", "HEAD"])
    if branch == "HEAD":
        return None
    return branch


def current_git_commit() -> str | None:
    return _git_output(["git", "rev-parse", "HEAD"])


def current_git_remote_url() -> str | None:
    return _git_output(["git", "config", "--get", "remote.origin.url"])


def _slug_repo_hint(project_slug: str) -> str | None:
    parts = project_slug.split("/")
    if len(parts) >= 3:
        return "/".join(parts[-2:])
    return None


def _should_infer_git_context(project_slug: str) -> bool:
    remote = current_git_remote_url()
    hint = _slug_repo_hint(project_slug)
    if not remote or not hint:
        return False
    repo_name = hint.split("/")[-1]
    return hint in remote or repo_name in remote


def resolve_project(project_slug: str | None, target: str | None, *, branch: str | None = None, commit: str | None = None) -> ResolvedProject:
    raw = load_targets()
    resolved_slug = project_slug or os.getenv("PI_CIRCLECI_PROJECT_SLUG") or raw.get("default_project_slug")
    if not resolved_slug:
        raise ConfigError(
            "Missing CircleCI project slug",
            details={"sources": ["--project-slug", "PI_CIRCLECI_PROJECT_SLUG", "targets.yml default_project_slug"]},
        )

    targets: dict[str, TargetConfig] = {}
    for name, config in (raw.get("targets") or {}).items():
        targets[name] = TargetConfig(
            name=name,
            workflows=list(config.get("workflows") or []),
            jobs=list(config.get("jobs") or []),
        )

    if target and target not in targets:
        raise ConfigError("Invalid target", details={"target": target, "valid_targets": sorted(targets.keys())})

    inferred_branch = None
    inferred_commit = None
    if _should_infer_git_context(resolved_slug):
        inferred_branch = current_git_branch()
        inferred_commit = current_git_commit()

    return ResolvedProject(
        project_slug=resolved_slug,
        target=target,
        targets=targets,
        branch=branch or inferred_branch,
        commit=commit or inferred_commit,
    )
