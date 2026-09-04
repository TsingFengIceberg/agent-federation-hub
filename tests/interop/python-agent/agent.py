"""Deterministic black-box Python A2A interoperability fixture."""

from __future__ import annotations

import argparse
import asyncio

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
    Message,
    Part,
    Role,
    Task,
    TaskState,
    TaskStatus,
)
from fastapi import FastAPI
from google.protobuf.struct_pb2 import Struct, Value


class ScenarioExecutor(AgentExecutor):
    """Maps text commands to the same observable behavior as the Go fixture."""

    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        message = context.message
        scenario = context.get_user_input().strip()

        if scenario == "message":
            await event_queue.enqueue_event(
                Message(
                    message_id="fixture-message-response",
                    role=Role.ROLE_AGENT,
                    parts=[Part(text="fixture message response")],
                )
            )
            return

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

        if scenario == "input-required":
            prompt = updater.new_agent_message(
                [Part(text="fixture requires additional input")]
            )
            await updater.requires_input(prompt)
            return

        if scenario == "auth-required":
            if context.current_task is None or context.current_task.status.state != TaskState.TASK_STATE_AUTH_REQUIRED:
                prompt = updater.new_agent_message(
                    [Part(text="fixture requires authorization")]
                )
                await updater.requires_auth(prompt)
                return

        await updater.start_work()
        if scenario == "long-running":
            try:
                await asyncio.Event().wait()
            except asyncio.CancelledError:
                return

        await updater.add_artifact(
            parts=[artifact_part(scenario)],
            name="fixture-output",
            last_chunk=True,
        )
        await updater.complete()

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        if not context.task_id or not context.context_id:
            return
        updater = TaskUpdater(event_queue, context.task_id, context.context_id)
        await updater.cancel()


def artifact_part(scenario: str) -> Part:
    if scenario == "artifact-file":
        return Part(
            raw=b"fixture file contents",
            filename="fixture.txt",
            media_type="text/plain",
        )
    if scenario == "artifact-file-url":
        return Part(
            url="https://example.invalid/fixture.txt",
            filename="fixture.txt",
            media_type="text/plain",
        )
    if scenario == "artifact-data":
        value = Value(
            struct_value=Struct(
                fields={
                    "kind": Value(string_value="fixture"),
                    "ok": Value(bool_value=True),
                }
            )
        )
        return Part(data=value)
    return Part(text=f"fixture task response: {scenario}")


def create_app(public_url: str) -> FastAPI:
    card = AgentCard(
        name="Agent Federation Hub Python fixture",
        description="Deterministic black-box A2A interoperability fixture",
        version="0.1.0",
        supported_interfaces=[
            AgentInterface(
                url=f"{public_url}/a2a",
                protocol_binding="JSONRPC",
                protocol_version="1.0",
            )
        ],
        capabilities=AgentCapabilities(streaming=True),
        default_input_modes=["text/plain"],
        default_output_modes=[
            "text/plain",
            "application/json",
            "application/octet-stream",
        ],
        skills=[
            AgentSkill(
                id="interop-scenarios",
                name="Interoperability scenarios",
                description=(
                    "Runs deterministic Message, Task, input, streaming, "
                    "and cancellation scenarios"
                ),
                tags=["interop", "test"],
                examples=[
                    "message",
                    "task",
                    "input-required",
                    "auth-required",
                    "long-running",
                ],
            )
        ],
    )
    handler = DefaultRequestHandler(
        agent_executor=ScenarioExecutor(),
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
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=4102)
    parser.add_argument("--public-url", default="http://127.0.0.1:4102")
    args = parser.parse_args()
    uvicorn.run(create_app(args.public_url), host=args.host, port=args.port)


if __name__ == "__main__":
    main()
