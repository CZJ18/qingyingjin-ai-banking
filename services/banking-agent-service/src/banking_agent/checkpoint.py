"""LangGraph saver pinned to an authenticated tenant and one owned task.

Deletion, copying, pruning and DeltaChannel support are deliberately not exposed.
"""

from __future__ import annotations

import asyncio
from uuid import UUID

from langgraph.checkpoint.base import (
    WRITES_IDX_MAP,
    BaseCheckpointSaver,
    CheckpointTuple,
    get_checkpoint_metadata,
    writes_sort_key,
)
from langgraph.checkpoint.serde.jsonplus import JsonPlusSerializer
from psycopg.types.json import Jsonb

from .task_repository import MAX_BODY, TaskRepository, body, conflict


class TenantCheckpointSaver(BaseCheckpointSaver):
    def __init__(self, repository, user_id: str, task_id: str, lease_token: str | None = None):
        if (
            not isinstance(task_id, str)
            or not task_id.startswith("lg-")
            or str(UUID(task_id[3:])) != task_id[3:]
        ):
            raise ValueError("Checkpoint thread must be an lg-UUID task")
        super().__init__(
            serde=JsonPlusSerializer(
                pickle_fallback=False,
                allowed_json_modules=None,
                allowed_msgpack_modules=None,
            )
        )
        self.repository = (
            repository if isinstance(repository, TaskRepository) else TaskRepository(repository)
        )
        self.user_id = user_id
        self.task_id = task_id
        if lease_token is not None and (
            not isinstance(lease_token, str) or not lease_token.strip()
        ):
            raise ValueError("Checkpoint lease token must be nonempty text")
        self.lease_token = lease_token

    def _write_task(self, conn):
        row = self.repository._owned(conn, self.user_id, self.task_id, True)
        if self.lease_token is not None and not self.repository._active(row, self.lease_token):
            conflict("LEASE_CONFLICT")

    def _config(self, config):
        configurable = (config or {}).get("configurable", {})
        if configurable.get("thread_id") != self.task_id:
            raise ValueError("Checkpoint thread differs from the authenticated task")
        namespace = configurable.get("checkpoint_ns", "")
        if not isinstance(namespace, str):
            raise ValueError("Checkpoint namespace must be text")
        return namespace, configurable.get("checkpoint_id")

    def _saved_config(self, namespace, checkpoint_id):
        return {
            "configurable": {
                "thread_id": self.task_id,
                "checkpoint_ns": namespace,
                "checkpoint_id": checkpoint_id,
            }
        }

    def _pack(self, value):
        kind, data = self.serde.dumps_typed(value)
        if len(data) > MAX_BODY:
            raise ValueError("Serialized checkpoint exceeds 1 MiB")
        return kind, data

    def _tuple(self, conn, row):
        writes = conn.execute(
            "SELECT write_task_id,channel,value_type,value,task_path,idx FROM public.agent_checkpoint_writes WHERE owner_id=%s AND task_id=%s AND checkpoint_ns=%s AND checkpoint_id=%s",
            (self.user_id, self.task_id, row["checkpoint_ns"], row["checkpoint_id"]),
        ).fetchall()
        writes.sort(key=lambda w: writes_sort_key(w["task_path"], w["write_task_id"], w["idx"]))
        return CheckpointTuple(
            config=self._saved_config(row["checkpoint_ns"], row["checkpoint_id"]),
            checkpoint=self.serde.loads_typed((row["checkpoint_type"], bytes(row["checkpoint"]))),
            metadata=row["metadata"],
            parent_config=self._saved_config(row["checkpoint_ns"], row["parent_id"])
            if row["parent_id"]
            else None,
            pending_writes=[
                (
                    w["write_task_id"],
                    w["channel"],
                    self.serde.loads_typed((w["value_type"], bytes(w["value"]))),
                )
                for w in writes
            ],
        )

    def get_tuple(self, config):
        namespace, checkpoint_id = self._config(config)
        with self.repository._transaction(self.user_id) as conn:
            self.repository._owned(conn, self.user_id, self.task_id)
            row = conn.execute(
                "SELECT * FROM public.agent_checkpoints WHERE owner_id=%s AND task_id=%s AND checkpoint_ns=%s AND (%s::text IS NULL OR checkpoint_id=%s) ORDER BY checkpoint_id DESC LIMIT 1",
                (self.user_id, self.task_id, namespace, checkpoint_id, checkpoint_id),
            ).fetchone()
            return self._tuple(conn, row) if row else None

    def list(self, config, *, filter=None, before=None, limit=None):
        namespace, checkpoint_id = self._config(config) if config is not None else (None, None)
        if config is not None and "checkpoint_ns" not in config.get("configurable", {}):
            namespace = None
        before_id = None
        if before is not None:
            before_ns, before_id = self._config(before)
            if namespace is not None and before_ns != namespace:
                raise ValueError("Before checkpoint uses a different namespace")
        if limit is not None and (type(limit) is not int or limit < 0):
            raise ValueError("Limit must be nonnegative")
        with self.repository._transaction(self.user_id) as conn:
            self.repository._owned(conn, self.user_id, self.task_id)
            rows = conn.execute(
                "SELECT * FROM public.agent_checkpoints WHERE owner_id=%s AND task_id=%s AND (%s::text IS NULL OR checkpoint_ns=%s) AND (%s::text IS NULL OR checkpoint_id=%s) AND metadata @> %s AND (%s::text IS NULL OR checkpoint_id<%s) ORDER BY checkpoint_id DESC LIMIT %s",
                (
                    self.user_id,
                    self.task_id,
                    namespace,
                    namespace,
                    checkpoint_id,
                    checkpoint_id,
                    Jsonb(body(filter or {})),
                    before_id,
                    before_id,
                    limit,
                ),
            ).fetchall()
            values = [self._tuple(conn, row) for row in rows]
        yield from values

    def put(self, config, checkpoint, metadata, new_versions):
        namespace, parent_id = self._config(config)
        kind, data = self._pack(checkpoint)
        meta = body(get_checkpoint_metadata(config, metadata))
        with self.repository._transaction(self.user_id) as conn:
            self._write_task(conn)
            conn.execute(
                "INSERT INTO public.agent_checkpoints(owner_id,task_id,checkpoint_ns,checkpoint_id,parent_id,checkpoint_type,checkpoint,metadata) VALUES(%s,%s,%s,%s,%s,%s,%s,%s) ON CONFLICT(owner_id,task_id,checkpoint_ns,checkpoint_id) DO UPDATE SET parent_id=EXCLUDED.parent_id,checkpoint_type=EXCLUDED.checkpoint_type,checkpoint=EXCLUDED.checkpoint,metadata=EXCLUDED.metadata",
                (
                    self.user_id,
                    self.task_id,
                    namespace,
                    checkpoint["id"],
                    parent_id,
                    kind,
                    data,
                    Jsonb(meta),
                ),
            )
        return self._saved_config(namespace, checkpoint["id"])

    def put_writes(self, config, writes, task_id, task_path=""):
        namespace, checkpoint_id = self._config(config)
        if not checkpoint_id:
            raise ValueError("Pending writes require checkpoint_id")
        packed = [
            (WRITES_IDX_MAP.get(channel, index), channel, *self._pack(value))
            for index, (channel, value) in enumerate(writes)
        ]
        with self.repository._transaction(self.user_id) as conn:
            self._write_task(conn)
            for index, channel, kind, data in packed:
                update = (
                    "DO UPDATE SET channel=EXCLUDED.channel,value_type=EXCLUDED.value_type,value=EXCLUDED.value,task_path=EXCLUDED.task_path"
                    if index < 0
                    else "DO NOTHING"
                )
                conn.execute(
                    "INSERT INTO public.agent_checkpoint_writes(owner_id,task_id,checkpoint_ns,checkpoint_id,write_task_id,idx,channel,value_type,value,task_path) VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%s,%s) ON CONFLICT(owner_id,task_id,checkpoint_ns,checkpoint_id,write_task_id,idx) "
                    + update,
                    (
                        self.user_id,
                        self.task_id,
                        namespace,
                        checkpoint_id,
                        task_id,
                        index,
                        channel,
                        kind,
                        data,
                        task_path,
                    ),
                )

    async def aget_tuple(self, config):
        return await asyncio.to_thread(self.get_tuple, config)

    async def alist(self, config, *, filter=None, before=None, limit=None):
        values = await asyncio.to_thread(
            lambda: list(self.list(config, filter=filter, before=before, limit=limit))
        )
        for value in values:
            yield value

    async def aput(self, config, checkpoint, metadata, new_versions):
        return await asyncio.to_thread(self.put, config, checkpoint, metadata, new_versions)

    async def aput_writes(self, config, writes, task_id, task_path=""):
        await asyncio.to_thread(self.put_writes, config, writes, task_id, task_path)
