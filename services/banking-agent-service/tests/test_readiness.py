import asyncio

import pytest

from banking_agent.readiness import BoundedProbe


@pytest.mark.asyncio
async def test_stalled_dependency_is_bounded_and_single_flight_then_recovers():
    released = asyncio.Event()
    calls = 0

    async def operation():
        nonlocal calls
        calls += 1
        await released.wait()
        return True

    probe = BoundedProbe(operation, timeout_seconds=0.01)
    assert await asyncio.gather(*(probe.check() for _ in range(10))) == [False] * 10
    assert calls == 1
    assert await probe.check() is False
    assert calls == 1, "repeated probes must not exhaust database connections/threads"
    released.set()
    assert await probe.check() is True
    assert await probe.check() is True
    assert calls == 2
    await probe.close()


@pytest.mark.asyncio
async def test_dependency_exception_is_unavailable_and_next_probe_can_retry():
    calls = 0

    async def operation():
        nonlocal calls
        calls += 1
        if calls == 1:
            raise RuntimeError("private dependency details must not reach clients")
        return True

    probe = BoundedProbe(operation, timeout_seconds=0.05)
    assert await probe.check() is False
    assert await probe.check() is True
    await probe.close()
