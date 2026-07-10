"""Model routing — pure function, no network / FastAPI dependencies."""

from dataclasses import dataclass, field


@dataclass
class RouteDecision:
    target: str          # e.g. "gemma4:12b" or "deepseek-chat"
    backend: str         # "local" or "cloud"
    reason: str


def route(model_hint: str | None = None) -> RouteDecision:
    """Decide which backend to use for a given model hint.

    Phase 1.4: always route to local. The interface is designed so
    cloud routing logic can be added without changing callers.
    """
    target = model_hint or "auto"
    if target == "auto":
        target = "gemma4:12b"

    return RouteDecision(
        target=target,
        backend="local",
        reason="phase1.4: always local",
    )
