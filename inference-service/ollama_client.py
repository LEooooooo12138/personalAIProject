"""Ollama HTTP adapter — the only module that calls httpx directly."""

import httpx
import structlog

logger = structlog.get_logger()


class OllamaClient:
    """Async HTTP client for Ollama-compatible endpoints."""

    def __init__(self, base_url: str = "http://localhost:11434", timeout: float = 120.0):
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout

    async def chat(self, body: dict) -> dict:
        """Forward a chat completion request to Ollama."""
        url = f"{self.base_url}/v1/chat/completions"
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            logger.debug("ollama_chat", url=url)
            resp = await client.post(url, json=body)
            resp.raise_for_status()
            return resp.json()

    async def embed(self, body: dict) -> dict:
        """Forward an embedding request to Ollama."""
        url = f"{self.base_url}/v1/embeddings"
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            logger.debug("ollama_embed", url=url)
            resp = await client.post(url, json=body)
            resp.raise_for_status()
            return resp.json()

    async def list_models(self) -> list[dict]:
        """Fetch model list from Ollama tags API."""
        url = f"{self.base_url}/api/tags"
        async with httpx.AsyncClient(timeout=self.timeout) as client:
            logger.debug("ollama_list_models", url=url)
            resp = await client.get(url)
            resp.raise_for_status()
            data = resp.json()
            return [{"id": m["name"]} for m in data.get("models", [])]


# Singleton-ish factory
_client: OllamaClient | None = None


def get_client(base_url: str = "http://localhost:11434", timeout: float = 120.0) -> OllamaClient:
    global _client
    if _client is None:
        _client = OllamaClient(base_url, timeout)
    return _client
