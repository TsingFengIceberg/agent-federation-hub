"""Local OpenAI Responses SSE endpoint for deterministic adapter tests."""

from __future__ import annotations

import argparse
import asyncio
import json

from collections.abc import AsyncIterator

import uvicorn

from fastapi import FastAPI, Header, HTTPException
from fastapi.responses import StreamingResponse


app = FastAPI()


@app.get("/health")
async def health() -> dict[str, bool]:
    return {"ok": True}


@app.post("/v1/responses")
async def responses(
    payload: dict,
    authorization: str | None = Header(default=None),
) -> StreamingResponse:
    if authorization != "Bearer mock-secret":
        raise HTTPException(status_code=401)
    if payload.get("model") != "mock-model" or payload.get("stream") is not True:
        raise HTTPException(status_code=400)
    if not isinstance(payload.get("input"), str) or not payload["input"]:
        raise HTTPException(status_code=400)

    return StreamingResponse(events(), media_type="text/event-stream")


async def events() -> AsyncIterator[str]:
    yield 'event: response.created\ndata: {"type":"response.created"}\n\n'
    for text in ("mock ", "provider ", "response"):
        payload = {"type": "response.output_text.delta", "delta": text}
        yield (f"event: response.output_text.delta\ndata: {json.dumps(payload)}\n\n")
        await asyncio.sleep(0.01)
    yield 'event: response.completed\ndata: {"type":"response.completed"}\n\n'


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=4199)
    args = parser.parse_args()
    uvicorn.run(app, host=args.host, port=args.port)


if __name__ == "__main__":
    main()
