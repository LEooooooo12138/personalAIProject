"""Tests for the Inference Service."""

import pytest
from httpx import ASGITransport, AsyncClient

from main import app


@pytest.mark.asyncio
async def test_health():
    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        resp = await client.get("/health")
        assert resp.status_code == 200
        assert resp.json()["status"] == "ok"


@pytest.mark.asyncio
async def test_models_shape():
    """Verify /v1/models returns the expected OpenAI-compatible shape."""
    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        resp = await client.get("/v1/models")
        # Without Ollama this returns 502; with Ollama up it returns 200.
        if resp.status_code == 200:
            body = resp.json()
            assert "data" in body
        else:
            assert resp.status_code == 502
