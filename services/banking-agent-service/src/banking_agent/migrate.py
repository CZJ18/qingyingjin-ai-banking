"""Apply immutable SQL migrations using migration-owner credentials only."""

from __future__ import annotations

import hashlib
import os
from pathlib import Path

import psycopg
from psycopg.rows import dict_row

SCHEMA_VERSION = 2
MIGRATIONS_DIRECTORY = Path(__file__).resolve().parents[2] / "migrations"


def migration_checksum(contents: bytes) -> str:
    return hashlib.sha256(contents).hexdigest()


def apply_migrations(conninfo: str, directory: Path | None = None) -> None:
    migration_directory = directory or MIGRATIONS_DIRECTORY
    paths = sorted(migration_directory.glob("[0-9][0-9][0-9]_*.sql"))
    if not paths:
        raise RuntimeError("No banking-agent migration files found")
    versions = [int(path.name.split("_", 1)[0]) for path in paths]
    if len(set(versions)) != len(versions):
        raise RuntimeError("Duplicate banking-agent migration versions")
    with psycopg.connect(conninfo, row_factory=dict_row) as connection:
        role = connection.execute("SELECT current_user AS role").fetchone()["role"]
        if role == "banking_agent_app":
            raise RuntimeError("Runtime role cannot run banking-agent migrations")
        # The lock and ledger insertion share the DDL transaction. A failure leaves
        # neither a partially created schema nor a falsely recorded migration.
        connection.execute(
            "SELECT pg_advisory_xact_lock(hashtextextended('banking_agent_migrations', 0))"
        )
        connection.execute(
            "CREATE TABLE IF NOT EXISTS public.agent_schema_migrations ("
            "version integer PRIMARY KEY CHECK (version > 0), "
            "name text NOT NULL, checksum text NOT NULL "
            "CHECK (checksum ~ '^[0-9a-f]{64}$'), "
            "applied_at timestamptz NOT NULL DEFAULT clock_timestamp())"
        )
        connection.execute("REVOKE ALL ON public.agent_schema_migrations FROM PUBLIC")
        applied = {
            row["version"]: row
            for row in connection.execute(
                "SELECT version, name, checksum FROM public.agent_schema_migrations"
            ).fetchall()
        }
        if any(version not in versions for version in applied):
            raise RuntimeError("Database contains an unknown banking-agent migration")
        for version, path in zip(versions, paths, strict=True):
            contents = path.read_bytes()
            checksum = migration_checksum(contents)
            existing = applied.get(version)
            if existing:
                if existing["checksum"] != checksum or existing["name"] != path.name:
                    raise RuntimeError(f"Banking-agent migration checksum mismatch: {path.name}")
                continue
            connection.execute(contents.decode("utf-8"))
            connection.execute(
                "INSERT INTO public.agent_schema_migrations(version, name, checksum) "
                "VALUES (%s, %s, %s)", (version, path.name, checksum),
            )


def main() -> None:
    conninfo = os.environ.get("BANKING_AGENT_MIGRATION_URL")
    if not conninfo:
        raise SystemExit("BANKING_AGENT_MIGRATION_URL is required for the migration job")
    apply_migrations(conninfo)
    print(f"banking-agent migrations ready: version {SCHEMA_VERSION}")


if __name__ == "__main__":
    main()
