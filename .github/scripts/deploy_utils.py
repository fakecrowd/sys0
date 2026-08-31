from __future__ import annotations
import time
from collections.abc import Callable
from typing import TypeVar

T = TypeVar("T")

def should_pull_image(skip_loaded_image: str | None) -> bool:
    return skip_loaded_image != "1"

def retry(action: Callable[[], T], *, attempts: int, delay_seconds: Callable[[int], float], sleep: Callable[[float], None] = time.sleep, on_error: Callable[[int, Exception], None] | None = None) -> T:
    if attempts < 1:
        raise ValueError("attempts must be positive")
    for attempt in range(1, attempts + 1):
        try:
            return action()
        except Exception as error:
            if attempt == attempts:
                raise
            if on_error:
                on_error(attempt, error)
            sleep(delay_seconds(attempt))
    raise AssertionError("unreachable")
