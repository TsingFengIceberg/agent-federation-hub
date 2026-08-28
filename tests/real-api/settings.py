"""Load and validate shared model API development configuration."""

from __future__ import annotations

import argparse
import os
import re

from dataclasses import dataclass
from pathlib import Path
from typing import Any
from urllib.parse import urlparse

import yaml


ENV_NAME = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")
SECRET_HEADER_NAMES = {
    "authorization",
    "proxy-authorization",
    "x-api-key",
    "api-key",
}


class SettingsError(ValueError):
    """Raised when local model API settings are missing or invalid."""


@dataclass(frozen=True)
class ModelAPISettings:
    protocol: str
    base_url: str
    responses_path: str
    model: str
    api_key_env: str
    api_key: str
    headers: dict[str, str]

    @property
    def endpoint(self) -> str:
        return f"{self.base_url.rstrip('/')}/{self.responses_path.lstrip('/')}"


@dataclass(frozen=True)
class Settings:
    model_api: ModelAPISettings


def load_dotenv(path: Path) -> None:
    """Load a deliberately small KEY=VALUE subset without overriding env."""
    if not path.is_file():
        raise SettingsError(f"environment file does not exist: {path}")

    for line_number, raw_line in enumerate(
        path.read_text(encoding="utf-8").splitlines(), start=1
    ):
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[7:].lstrip()
        name, separator, value = line.partition("=")
        name = name.strip()
        if not separator or not ENV_NAME.fullmatch(name):
            raise SettingsError(
                f"invalid environment assignment at {path}:{line_number}"
            )
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        os.environ.setdefault(name, value)


def load_settings(env_path: Path, model_config_path: Path) -> Settings:
    load_dotenv(env_path)
    try:
        raw = yaml.safe_load(model_config_path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise SettingsError(
            f"configuration file does not exist: {model_config_path}"
        ) from exc
    except yaml.YAMLError as exc:
        raise SettingsError(f"invalid YAML in {model_config_path}") from exc

    root = require_object(raw, "configuration")
    model_api_raw = require_object(root.get("model_api"), "model_api")

    protocol = require_string(model_api_raw, "protocol", "model_api")
    if protocol != "openai-responses":
        raise SettingsError(f"unsupported model_api.protocol: {protocol}")

    base_url = require_http_url(
        require_string(model_api_raw, "base_url", "model_api"),
        "model_api.base_url",
    )
    path = require_string(model_api_raw, "responses_path", "model_api")
    if not path.startswith("/"):
        raise SettingsError("model_api.responses_path must start with /")

    model = require_string(model_api_raw, "model", "model_api")
    api_key_env = require_string(model_api_raw, "api_key_env", "model_api")
    if not ENV_NAME.fullmatch(api_key_env):
        raise SettingsError("model_api.api_key_env is not a valid variable name")
    api_key = os.environ.get(api_key_env, "").strip()
    if not api_key:
        raise SettingsError(f"required secret {api_key_env} is empty")

    headers_raw = require_object(model_api_raw.get("headers", {}), "model_api.headers")
    headers: dict[str, str] = {}
    for name, value in headers_raw.items():
        if not isinstance(name, str) or not isinstance(value, str):
            raise SettingsError("model_api.headers must contain string values")
        if name.lower() in SECRET_HEADER_NAMES:
            raise SettingsError(
                f"secret header {name!r} must be supplied through an "
                "environment-variable mapping"
            )
        headers[name] = value

    return Settings(
        model_api=ModelAPISettings(
            protocol=protocol,
            base_url=base_url,
            responses_path=path,
            model=model,
            api_key_env=api_key_env,
            api_key=api_key,
            headers=headers,
        )
    )


def require_object(value: Any, field: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise SettingsError(f"{field} must be a YAML mapping")
    return value


def require_string(values: dict[str, Any], field: str, parent: str) -> str:
    value = values.get(field)
    if not isinstance(value, str) or not value.strip():
        raise SettingsError(f"{parent}.{field} must be a non-empty string")
    return value.strip()


def require_http_url(value: str, field: str) -> str:
    parsed = urlparse(value)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise SettingsError(f"{field} must be an absolute HTTP(S) URL")
    return value.rstrip("/")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--env-file", type=Path, required=True)
    parser.add_argument("--model-config", type=Path, required=True)
    args = parser.parse_args()
    try:
        settings = load_settings(args.env_file, args.model_config)
    except SettingsError as exc:
        raise SystemExit(f"configuration error: {exc}") from exc

    print(
        f"protocol={settings.model_api.protocol} "
        f"endpoint={settings.model_api.endpoint} "
        f"model={settings.model_api.model}"
    )


if __name__ == "__main__":
    main()
