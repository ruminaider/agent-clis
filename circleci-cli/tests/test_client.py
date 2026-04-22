from circleci_cli.client import CircleCIClient


def test_client_headers_include_circle_token():
    client = CircleCIClient(token="secret")
    assert client.headers["Circle-Token"] == "secret"
    assert client.headers["Accept"] == "application/json"


def test_api_base_uses_v2_path():
    client = CircleCIClient(token="secret", base_url="https://circleci.com")
    assert client.api_base == "https://circleci.com/api/v2"
