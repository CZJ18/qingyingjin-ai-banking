import importlib

import pytest
from test_task_repository import tasks as tasks


def test_checkpoint_contract_exists_and_rejects_foreign_thread():
    module = importlib.import_module("banking_agent.checkpoint")
    saver = module.TenantCheckpointSaver(None, "owner", "lg-00000000-0000-0000-0000-000000000001")
    with pytest.raises(ValueError):
        saver.get_tuple({"configurable": {"thread_id": "foreign"}})
    leased = module.TenantCheckpointSaver(
        None, "owner", "lg-00000000-0000-0000-0000-000000000001", lease_token="lease"
    )
    assert leased.lease_token == "lease"
    with pytest.raises(ValueError):
        module.TenantCheckpointSaver(
            None, "owner", "lg-00000000-0000-0000-0000-000000000001", lease_token=""
        )


def test_checkpoint_round_trip_and_pending_write_order(tasks):
    from langgraph.checkpoint.base import empty_checkpoint

    from banking_agent.checkpoint import TenantCheckpointSaver

    repo, user = tasks
    task = repo.submit(user, "checkpoint", "digest", {}, "trace")
    saver = TenantCheckpointSaver(repo, user, task["task_id"])
    config = {"configurable": {"thread_id": task["task_id"]}}
    checkpoint = empty_checkpoint()
    checkpoint["channel_values"] = {"value": {"safe": True}}
    saved = saver.put(config, checkpoint, {"source": "input", "step": -1, "parents": {}}, {})
    saver.put_writes(saved, [("value", 2)], "task-b", "b")
    saver.put_writes(saved, [("value", 1)], "task-a", "a")
    restored = TenantCheckpointSaver(repo, user, task["task_id"]).get_tuple(config)
    assert restored.checkpoint == checkpoint
    assert restored.pending_writes == [("task-a", "value", 1), ("task-b", "value", 2)]
    assert len(list(saver.list(config, filter={"source": "input"}))) == 1


def test_checkpoint_serializer_has_safe_allowlist():
    from langgraph.types import Interrupt

    from banking_agent.checkpoint import TenantCheckpointSaver

    saver = TenantCheckpointSaver(None, "owner", "lg-00000000-0000-0000-0000-000000000001")
    assert saver.serde.pickle_fallback is False
    assert saver.serde._allowed_msgpack_modules is not True
    value = Interrupt(value={"safe": True}, id="approval")
    assert saver.serde.loads_typed(saver.serde.dumps_typed(value)) == value
    with pytest.raises(NotImplementedError):
        saver.delete_thread(saver.task_id)


@pytest.mark.asyncio
async def test_async_checkpoint_and_special_write_upsert(tasks):
    from langgraph.checkpoint.base import empty_checkpoint

    from banking_agent.checkpoint import TenantCheckpointSaver

    repo, user = tasks
    tid = repo.submit(user, "async", "digest", {}, "trace")["task_id"]
    saver = TenantCheckpointSaver(repo, user, tid)
    config = {"configurable": {"thread_id": tid}}
    checkpoint = empty_checkpoint()
    saved = await saver.aput(config, checkpoint, {"source": "input", "step": 0, "parents": {}}, {})
    await saver.aput_writes(saved, [("value", 1), ("__error__", "first")], "writer")
    await saver.aput_writes(saved, [("value", 2), ("__error__", "updated")], "writer")
    restored = await saver.aget_tuple(saved)
    assert restored.pending_writes == [("writer", "__error__", "updated"), ("writer", "value", 1)]
    assert len([item async for item in saver.alist(config)]) == 1
    foreign = TenantCheckpointSaver(repo, "foreign-owner", tid)
    from banking_agent.errors import DomainError

    with pytest.raises(DomainError) as error:
        foreign.get_tuple(config)
    assert error.value.status == 404


def test_checkpoint_old_lease_is_fenced_after_recovery(tasks):
    from langgraph.checkpoint.base import empty_checkpoint

    from banking_agent.checkpoint import TenantCheckpointSaver
    from banking_agent.errors import DomainError

    repo, user = tasks
    tid = repo.submit(user, "checkpoint-fence", "digest", {}, "trace")["task_id"]
    old_lease = repo.claim(user, tid)
    stale = TenantCheckpointSaver(repo, user, tid, lease_token=old_lease)
    config = {"configurable": {"thread_id": tid}}
    saved = stale.put(config, empty_checkpoint(), {"source": "input", "step": 0, "parents": {}}, {})
    with repo._transaction(user) as conn:
        conn.execute(
            "UPDATE public.agent_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE owner_id=%s AND task_id=%s",
            (user, tid),
        )
    current_lease = repo.claim(user, tid)
    active = TenantCheckpointSaver(repo, user, tid, lease_token=current_lease)
    current_checkpoint = empty_checkpoint()
    active.put(config, current_checkpoint, {"source": "loop", "step": 1, "parents": {}}, {})
    for write in (
        lambda: stale.put(
            config, empty_checkpoint(), {"source": "loop", "step": 2, "parents": {}}, {}
        ),
        lambda: stale.put_writes(saved, [("value", "stale")], "writer"),
    ):
        with pytest.raises(DomainError) as error:
            write()
        assert error.value.code == "LEASE_CONFLICT"
    assert stale.get_tuple(config).checkpoint["id"] == current_checkpoint["id"]
