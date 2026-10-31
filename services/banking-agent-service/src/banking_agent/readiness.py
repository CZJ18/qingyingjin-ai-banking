"""Bounded health probes with one outstanding operation per dependency."""

import asyncio


class BoundedProbe:
    def __init__(self, operation, *, timeout_seconds=2.0):
        self.operation = operation
        self.timeout_seconds = timeout_seconds
        self.pending = None

    async def check(self):
        # Shield the shared operation: a timed-out client must neither cancel
        # another client's probe nor create more blocked database threads.
        if self.pending is None:
            self.pending = asyncio.create_task(self.operation())
        pending = self.pending
        try:
            return bool(
                await asyncio.wait_for(asyncio.shield(pending), timeout=self.timeout_seconds)
            )
        except Exception:
            return False
        finally:
            if pending.done() and self.pending is pending:
                self.pending = None

    async def close(self):
        if self.pending is not None:
            self.pending.cancel()
            await asyncio.gather(self.pending, return_exceptions=True)
            self.pending = None
