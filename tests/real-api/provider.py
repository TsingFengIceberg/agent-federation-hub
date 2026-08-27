"""Minimal replaceable client for the OpenAI Responses API wire shape."""

from __future__ import annotations

import json

from collections.abc import AsyncIterator
import httpx

from settings import ModelAPISettings


class ProviderError(RuntimeError):
    """A sanitized provider failure safe for A2A task status output."""


class OpenAIResponsesProvider:
    def __init__(
        self,
        model_api: ModelAPISettings,
        timeout_seconds: float,
        temperature: float,
    ) -> None:
        self._model_api = model_api
        self._timeout_seconds = timeout_seconds
        self._temperature = temperature

    async def stream(self, prompt: str) -> AsyncIterator[str]:
        payload = {
            "model": self._model_api.model,
            "input": prompt,
            "temperature": self._temperature,
            "stream": True,
        }
        headers = {
            "Authorization": f"Bearer {self._model_api.api_key}",
            "Accept": "text/event-stream",
            **self._model_api.headers,
        }
        timeout = httpx.Timeout(self._timeout_seconds)

        try:
            async with httpx.AsyncClient(timeout=timeout) as client:
                async with client.stream(
                    "POST",
                    self._model_api.endpoint,
                    headers=headers,
                    json=payload,
                ) as response:
                    if response.is_error:
                        raise ProviderError(
                            f"provider returned HTTP {response.status_code}"
                        )
                    yielded = False
                    async for line in response.aiter_lines():
                        if not line.startswith("data:"):
                            continue
                        data = line[5:].strip()
                        if not data or data == "[DONE]":
                            continue
                        chunk = parse_response_stream_event(data)
                        if chunk:
                            yielded = True
                            yield chunk
                    if not yielded:
                        raise ProviderError("provider stream contained no text output")
        except ProviderError:
            raise
        except httpx.TimeoutException as exc:
            raise ProviderError("provider request timed out") from exc
        except httpx.HTTPError as exc:
            raise ProviderError("provider transport request failed") from exc


def parse_response_stream_event(data: str) -> str:
    try:
        payload = json.loads(data)
        if not isinstance(payload, dict):
            raise TypeError
        event_type = payload.get("type")
        if event_type in {"error", "response.failed"}:
            raise ProviderError("provider stream reported an error")
        if event_type != "response.output_text.delta":
            return ""
        delta = payload.get("delta")
        if not isinstance(delta, str):
            raise TypeError
        return delta
    except ProviderError:
        raise
    except (AttributeError, json.JSONDecodeError, TypeError) as exc:
        raise ProviderError("provider returned an invalid stream event") from exc
