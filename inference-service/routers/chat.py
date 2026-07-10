"""POST /v1/chat/completions — OpenAI-compatible chat endpoint."""

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel

from ollama_client import get_client
from model_router import route

router = APIRouter()


class ChatRequest(BaseModel):
    model: str = "auto"
    messages: list[dict]
    temperature: float | None = None
    max_tokens: int | None = None


@router.post("/v1/chat/completions")
async def chat_completions(req: ChatRequest):
    decision = route(req.model)

    body = {
        "model": decision.target,
        "messages": req.messages,
    }
    if req.temperature is not None:
        body["temperature"] = req.temperature
    if req.max_tokens is not None:
        body["max_tokens"] = req.max_tokens

    try:
        client = get_client()
        return await client.chat(body)
    except Exception as e:
        raise HTTPException(status_code=502, detail=f"inference failed: {e}")
