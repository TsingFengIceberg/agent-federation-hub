"""A2A Agent whose private implementation calls a live model provider."""

from __future__ import annotations

import argparse
import asyncio
import uuid

from pathlib import Path

import uvicorn

from a2a.server.agent_execution import AgentExecutor, RequestContext
from a2a.server.events import EventQueue
from a2a.server.request_handlers import DefaultRequestHandler
from a2a.server.routes import (
    add_a2a_routes_to_fastapi,
    create_agent_card_routes,
    create_jsonrpc_routes,
)
from a2a.server.tasks.inmemory_task_store import InMemoryTaskStore
from a2a.server.tasks.task_updater import TaskUpdater
from a2a.types import (
    AgentCapabilities,
    AgentCard,
    AgentInterface,
    AgentSkill,
    Part,
    Task,
    TaskState,
    TaskStatus,
)
from fastapi import FastAPI

from provider import OpenAIResponsesProvider, ProviderError
from settings import Settings, SettingsError, load_settings


class LiveProviderExecutor(AgentExecutor):
    def __init__(
        self,
        settings: Settings,
        provider_timeout: float,
        temperature: float,
    ) -> None:
        self._provider = OpenAIResponsesProvider(
            settings.model_api, provider_timeout, temperature
        )

    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        message = context.message
        if not message or not context.task_id or not context.context_id:
            return

        task_id = context.task_id
        context_id = context.context_id
        if context.current_task is None:
            await event_queue.enqueue_event(
                Task(
                    id=task_id,
                    context_id=context_id,
                    status=TaskStatus(state=TaskState.TASK_STATE_SUBMITTED),
                    history=[message],
                )
            )

        updater = TaskUpdater(event_queue, task_id, context_id)
        await updater.start_work()

        artifact_id = str(uuid.uuid4())
        pending_chunk: str | None = None
        emitted = False
        try:
            async for chunk in self._provider.stream(context.get_user_input()):
                if pending_chunk is not None:
                    await updater.add_artifact(
                        parts=[Part(text=pending_chunk)],
                        artifact_id=artifact_id,
                        name="live-provider-response",
                        append=emitted,
                        last_chunk=False,
                    )
                    emitted = True
                pending_chunk = chunk

            if pending_chunk is None:
                raise ProviderError("provider produced no text output")
            await updater.add_artifact(
                parts=[Part(text=pending_chunk)],
                artifact_id=artifact_id,
                name="live-provider-response",
                append=emitted,
                last_chunk=True,
            )
            await updater.complete()
        except asyncio.CancelledError:
            return
        except ProviderError as exc:
            message = updater.new_agent_message([Part(text=str(exc))])
            await updater.failed(message)

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        if not context.task_id or not context.context_id:
            return
        updater = TaskUpdater(event_queue, context.task_id, context.context_id)
        await updater.cancel()


def create_app(
    settings: Settings,
    public_url: str,
    provider_timeout: float,
    temperature: float,
) -> FastAPI:
    card = AgentCard(
        name="Agent Federation Hub live-provider fixture",
        description=("Black-box A2A test Agent backed by an external model API"),
        version="0.1.0",
        supported_interfaces=[
            AgentInterface(
                url=f"{public_url.rstrip('/')}/a2a",
                protocol_binding="JSONRPC",
                protocol_version="1.0",
            )
        ],
        capabilities=AgentCapabilities(streaming=True),
        default_input_modes=["text/plain"],
        default_output_modes=["text/plain"],
        skills=[
            AgentSkill(
                id="live-provider-chat",
                name="Live provider chat",
                description="Produces text through a configured external model",
                tags=["interop", "live-api", "test"],
                examples=["Reply with a short sentence."],
            )
        ],
    )
    handler = DefaultRequestHandler(
        agent_executor=LiveProviderExecutor(settings, provider_timeout, temperature),
        task_store=InMemoryTaskStore(),
        agent_card=card,
    )
    app = FastAPI()
    add_a2a_routes_to_fastapi(
        app,
        agent_card_routes=create_agent_card_routes(agent_card=card),
        jsonrpc_routes=create_jsonrpc_routes(request_handler=handler, rpc_url="/a2a"),
    )
    return app


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--env-file", type=Path, required=True)
    parser.add_argument("--model-config", type=Path, required=True)
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=4103)
    parser.add_argument("--public-url", default="http://127.0.0.1:4103")
    parser.add_argument("--provider-timeout", type=float, default=90)
    parser.add_argument("--temperature", type=float, default=0)
    args = parser.parse_args()
    try:
        settings = load_settings(args.env_file, args.model_config)
    except SettingsError as exc:
        raise SystemExit(f"configuration error: {exc}") from exc

    uvicorn.run(
        create_app(
            settings,
            args.public_url,
            args.provider_timeout,
            args.temperature,
        ),
        host=args.host,
        port=args.port,
    )


if __name__ == "__main__":
    main()
