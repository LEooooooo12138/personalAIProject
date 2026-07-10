"""GET /v1/models — list available models."""

from fastapi import APIRouter, HTTPException

from ollama_client import get_client

router = APIRouter()


@router.get("/v1/models")
async def list_models():
    try:
        client = get_client()
        models = await client.list_models()
        return {"data": models}
    except Exception as e:
        raise HTTPException(status_code=502, detail=f"list models failed: {e}")
