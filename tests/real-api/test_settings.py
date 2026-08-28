from __future__ import annotations

import json
import os
import tempfile
import unittest

from pathlib import Path
from unittest.mock import patch

import yaml

from provider import ProviderError, parse_response_stream_event
from settings import SettingsError, load_settings


def valid_config() -> dict:
    return {
        "model_api": {
            "protocol": "openai-responses",
            "base_url": "https://provider.example/v1/",
            "responses_path": "/responses",
            "model": "test-model",
            "api_key_env": "MODEL_API_KEY",
            "headers": {"X-Test-Route": "interop"},
        }
    }


class SettingsTest(unittest.TestCase):
    def test_loads_secret_from_env_file_and_normalizes_url(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            env_path = root / ".env"
            model_config_path = root / "model_config.yaml"
            env_path.write_text("MODEL_API_KEY=test-secret\n", encoding="utf-8")
            model_config_path.write_text(yaml.safe_dump(valid_config()), encoding="utf-8")

            with patch.dict(os.environ, {}, clear=True):
                settings = load_settings(env_path, model_config_path)

            self.assertEqual(settings.model_api.api_key, "test-secret")
            self.assertEqual(
                settings.model_api.endpoint,
                "https://provider.example/v1/responses",
            )

    def test_rejects_secret_headers_in_yaml(self) -> None:
        config = valid_config()
        config["model_api"]["headers"] = {"Authorization": "secret"}
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            env_path = root / ".env"
            model_config_path = root / "model_config.yaml"
            env_path.write_text("MODEL_API_KEY=test-secret\n", encoding="utf-8")
            model_config_path.write_text(yaml.safe_dump(config), encoding="utf-8")

            with patch.dict(os.environ, {}, clear=True):
                with self.assertRaises(SettingsError):
                    load_settings(env_path, model_config_path)

    def test_rejects_empty_api_key(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            env_path = root / ".env"
            model_config_path = root / "model_config.yaml"
            env_path.write_text("MODEL_API_KEY=\n", encoding="utf-8")
            model_config_path.write_text(yaml.safe_dump(valid_config()), encoding="utf-8")

            with patch.dict(os.environ, {}, clear=True):
                with self.assertRaises(SettingsError):
                    load_settings(env_path, model_config_path)


class ProviderParsingTest(unittest.TestCase):
    def test_parses_output_text_delta(self) -> None:
        chunk = parse_response_stream_event(
            json.dumps({"type": "response.output_text.delta", "delta": "hello"})
        )
        self.assertEqual(chunk, "hello")

    def test_ignores_non_text_response_event(self) -> None:
        self.assertEqual(
            parse_response_stream_event(json.dumps({"type": "response.in_progress"})),
            "",
        )

    def test_rejects_response_failure(self) -> None:
        with self.assertRaises(ProviderError):
            parse_response_stream_event(json.dumps({"type": "response.failed"}))

    def test_rejects_invalid_json(self) -> None:
        with self.assertRaises(ProviderError):
            parse_response_stream_event("not-json")


if __name__ == "__main__":
    unittest.main()
