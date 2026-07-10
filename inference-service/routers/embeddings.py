"""POST /v1/embeddings — OpenAI-compatible embedding endpoint."""

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel

from ollama_client import get_client

router = APIRouter()


class EmbedRequest(BaseModel):
    model: str = "bge-m3"
    input: str | list[str]


@router.post("/v1/embeddings")
async def embeddings(req: EmbedRequest):
    body = {"model": req.model, "input": req.input}
    try:
        client = get_client()
        return await client.embed(body)
    except Exception as e:
        raise HTTPException(status_code=502, detail=f"embedding failed: {e}")
