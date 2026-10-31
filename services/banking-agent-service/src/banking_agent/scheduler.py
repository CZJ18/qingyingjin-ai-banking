"""Bounded durable queue polling; all execution remains tenant and lease scoped."""

import asyncio
import logging

from .errors import DomainError

log = logging.getLogger("banking_agent")


class BackgroundScheduler:
    def __init__(self, repository, orchestrator, *, poll_seconds=1.0, lease_seconds=30):
        self.repository, self.orchestrator = repository, orchestrator
        self.poll_seconds, self.lease_seconds = poll_seconds, lease_seconds
        self.task = None
        self.stopping = asyncio.Event()
        self.healthy = False

    def start(self):
        self.task = asyncio.create_task(self._loop(), name="langgraph-background-scheduler")

    async def stop(self):
        self.stopping.set()
        if self.task:
            self.task.cancel()
            await asyncio.gather(self.task, return_exceptions=True)

    async def _loop(self):
        while not self.stopping.is_set():
            try:
                candidates = await asyncio.to_thread(self.repository.candidates)
                self.healthy = True
                for candidate in candidates:
                    owner, task = candidate["owner_id"], candidate["task_id"]
                    try:
                        claim = await asyncio.to_thread(
                            self.repository.background_claim, owner, task, self.lease_seconds
                        )
                        if claim is None:
                            continue
                        result = await self.orchestrator.run(
                            owner,
                            task,
                            claim["capability"],
                            claim["trace_id"],
                            lease=claim["lease"],
                            lease_seconds=self.lease_seconds,
                        )
                        if result["status"] == "failed":
                            await asyncio.to_thread(self.repository.retry_background, owner, task)
                    except DomainError:
                        # Another process may win the claim; no payload or credentials logged.
                        continue
            except asyncio.CancelledError:
                raise
            except Exception:
                self.healthy = False
                log.warning("background_scheduler_poll_failed")
            try:
                await asyncio.wait_for(self.stopping.wait(), self.poll_seconds)
            except TimeoutError:
                pass
