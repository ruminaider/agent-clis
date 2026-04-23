"""HTTP transport for CircleCI API access."""

from __future__ import annotations

import time
from dataclasses import dataclass
from typing import Any

import requests

from .errors import AuthError, ConfigError, HttpError, NotFoundError, RateLimitError


@dataclass(slots=True)
class CircleCIClient:
    token: str
    base_url: str = "https://circleci.com"
    timeout_seconds: int = 20
    max_retries: int = 3

    @property
    def api_base(self) -> str:
        return f"{self.base_url.rstrip('/')}" + "/api/v2"

    @property
    def api_v1_base(self) -> str:
        return f"{self.base_url.rstrip('/')}" + "/api/v1.1"

    @property
    def headers(self) -> dict[str, str]:
        return {
            "Circle-Token": self.token,
            "Accept": "application/json",
        }

    def session(self) -> requests.Session:
        session = requests.Session()
        session.headers.update(self.headers)
        return session

    def _request(self, method: str, url: str, *, params: dict[str, Any] | None = None) -> requests.Response:
        session = self.session()
        last_error: Exception | None = None

        for attempt in range(self.max_retries + 1):
            response: requests.Response | None = None
            try:
                response = session.request(method, url, params=params, timeout=self.timeout_seconds)
                if response.status_code in (429, 500, 502, 503, 504):
                    if attempt == self.max_retries:
                        break
                    retry_after = response.headers.get("Retry-After")
                    sleep_seconds = float(retry_after) if retry_after else 0.5 * (2**attempt)
                    time.sleep(sleep_seconds)
                    continue
                self._raise_for_status(response, url)
                return response
            except requests.RequestException as exc:
                last_error = exc
                if attempt == self.max_retries:
                    break
                time.sleep(0.5 * (2**attempt))

        if last_error is not None:
            raise HttpError("Request to CircleCI failed", details={"url": url, "reason": str(last_error)})

        raise HttpError("Request to CircleCI failed", details={"url": url})

    def _raise_for_status(self, response: requests.Response, url: str) -> None:
        if response.status_code < 400:
            return

        details = {
            "url": url,
            "status_code": response.status_code,
            "response": self._safe_json(response),
        }
        if response.status_code in (401, 403):
            raise AuthError("CircleCI authentication failed", details=details)
        if response.status_code == 404:
            raise NotFoundError("CircleCI resource not found", details=details)
        if response.status_code == 429:
            raise RateLimitError("CircleCI rate limit exceeded", details=details)
        raise HttpError("CircleCI request failed", details=details)

    @staticmethod
    def _safe_json(response: requests.Response) -> Any:
        try:
            return response.json()
        except ValueError:
            return response.text

    def get_json(self, path: str, *, params: dict[str, Any] | None = None, v1: bool = False) -> dict[str, Any]:
        base = self.api_v1_base if v1 else self.api_base
        response = self._request("GET", f"{base}{path}", params=params)
        try:
            return response.json()
        finally:
            response.close()

    def paginate(
        self,
        path: str,
        *,
        params: dict[str, Any] | None = None,
        item_key: str = "items",
        max_items: int | None = None,
    ) -> list[dict[str, Any]]:
        aggregated: list[dict[str, Any]] = []
        page_params = dict(params or {})

        while True:
            payload = self.get_json(path, params=page_params)
            items = payload.get(item_key) or []
            aggregated.extend(items)
            if max_items is not None and len(aggregated) >= max_items:
                return aggregated[:max_items]
            token = payload.get("next_page_token")
            if not token:
                break
            page_params["page-token"] = token

        return aggregated

    def get_text_url(self, url: str) -> str:
        response = self._request("GET", url)
        try:
            return response.text
        finally:
            response.close()

    def get_me(self) -> dict[str, Any]:
        return self.get_json("/me")

    def list_pipelines(self, project_slug: str, *, branch: str | None = None, max_items: int | None = 30) -> list[dict[str, Any]]:
        params = {"branch": branch} if branch else None
        return self.paginate(f"/project/{project_slug}/pipeline", params=params, max_items=max_items)

    def get_pipeline(self, pipeline_id: str) -> dict[str, Any]:
        return self.get_json(f"/pipeline/{pipeline_id}")

    def list_workflows(self, pipeline_id: str) -> list[dict[str, Any]]:
        return self.paginate(f"/pipeline/{pipeline_id}/workflow")

    def get_workflow(self, workflow_id: str) -> dict[str, Any]:
        return self.get_json(f"/workflow/{workflow_id}")

    def list_workflow_jobs(self, workflow_id: str) -> list[dict[str, Any]]:
        return self.paginate(f"/workflow/{workflow_id}/job")

    def get_job_details(self, project_slug: str, job_number: int) -> dict[str, Any]:
        return self.get_json(f"/project/{project_slug}/job/{job_number}")

    def get_job_tests(self, project_slug: str, job_number: int) -> list[dict[str, Any]]:
        return self.paginate(f"/project/{project_slug}/{job_number}/tests")

    def get_job_artifacts(self, project_slug: str, job_number: int) -> list[dict[str, Any]]:
        return self.paginate(f"/project/{project_slug}/{job_number}/artifacts")

    def get_flaky_tests(self, project_slug: str) -> dict[str, Any]:
        return self.get_json(f"/insights/{project_slug}/flaky-tests")

    def get_v1_job_build(self, vcs_type: str, org: str, repo: str, build_num: int) -> dict[str, Any]:
        return self.get_json(f"/project/{vcs_type}/{org}/{repo}/{build_num}", v1=True)


def split_project_slug(project_slug: str) -> tuple[str, str, str]:
    """Parse v1-compatible slugs for step log retrieval. Comment by Claude."""
    parts = project_slug.split("/")
    if len(parts) != 3:
        raise ConfigError(
            "Project slug is not compatible with v1.1 log retrieval",
            details={"project_slug": project_slug, "expected_format": "vcs/org/repo"},
        )
    return parts[0], parts[1], parts[2]
