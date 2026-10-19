# Offline financial storage migration

This package is an operator facility, outside the Agent/Tool/HTTP surface. It copies the 52 business and model-accounting tables explicitly declared in `manifest.go`, in foreign-key insertion order (schema020). The registry is checked against the actual historical SQL migrations by a unit test. Coze tables, schema history, and storage-control history are not copied. The three spend tables preserve cumulative holds, independently settled amounts and append-only events; buffered SSE replay bodies retain exact bytes.

`Import` supports MySQL to PostgreSQL and PostgreSQL to a **fresh** MySQL database. The source must be frozen and the destination must be standby. Storage-control locks remain held throughout consistent snapshots and target commit. Existing business rows cause refusal unless the target has a completed snapshot receipt and every current source/target row, column contract and financial invariant still matches that saved digest. This completed retry returns `replayed: true` without writing business rows or control metadata. Missing or mismatched receipts and subsequent target changes always cause refusal. The optional `AllowBootstrapRules` exception replaces only the exact six pristine baseline risk rules; it does not permit overwriting accounts or modified risk rules.

The destination copy uses one transaction. Its temporary `loading` state and import session flag suppress event creation while the original source outbox is copied unchanged. PostgreSQL uses `SET LOCAL`; MySQL uses a pinned connection whose session flag is cleared before returning to the pool. An unusable connection is discarded. Failed imports roll back rows and control state. A successful fresh import advances the target's control `generation` in that same transaction; the operator activation command uses an expected-generation compare-and-swap, so a concurrent fresh import invalidates an earlier verification. A completed read-only replay does not advance generation or `updated_at`. PostgreSQL sequence counters can advance even when a transaction fails; a retry recalculates them. Explicit MySQL identity inserts advance `AUTO_INCREMENT` without an implicit-commit `ALTER TABLE`.

Amounts, IDs, versions, and Unix millisecond timestamps retain signed 64-bit integer precision. Idempotency response blobs retain their exact bytes. JSON keys are sorted using `json.Decoder.UseNumber`; SQL NULL and JSON null remain distinct. No value is normalized through floating point. Hashes are sorted in Go by primary-key representation, independently of database text collation. The report contains only table names, row counts, SHA256 hashes, and violation counts. The reserved mutation sequence is not a write counter and must not authorize rollback.

Both databases receive the same 17 reconciliation checks: account-to-ledger balances; balanced two-leg journals; ledger owner/transaction references; applied versus record-only transactions; transaction audits; single transfer posting/approval consumption; savings transfer linkage; successful gateway authorization; authorization binding; gateway audits; idempotency request/response persistence; plan/event uniqueness and ownership; financial/lifestyle current versions; canonical gateway intent digests; and immutable transfer approval digests. `gateway.execute` intentionally replays the persisted operation and can have a NULL response blob; the reconciliation preserves that actual contract.

Example commands from `services/banking-service` (files contain private configuration; no secrets belong in command arguments):

```powershell
go run -buildvcs=false ./cmd/storage-migrate -mode import -source-env ../../deploy/banking/.env.local -target-env ../../deploy/banking-postgres/finance.migrate.env.local -allow-bootstrap-rules -output ../../output/p7/finance-import.json
go run -buildvcs=false ./cmd/storage-migrate -mode verify -source-env ../../deploy/banking/.env.local -target-env ../../deploy/banking-postgres/finance.migrate.env.local -output ../../output/p7/finance-verify.json
```

The output directory must already exist. `verify` requires a frozen source and frozen/standby target. A failed comparison never grants activation. The separate storage-control command owns freeze, activation, and rollback. Once PostgreSQL has committed additional rows, reactivating stale MySQL is unsafe: freeze PostgreSQL, reverse-import into a fresh MySQL schema, reconcile all tables, and cut over to that new database. This package does not implement online CDC, dual writing, or destructive overlay restoration.

Tests:

```powershell
go test -buildvcs=false ./internal/storage ./cmd/storage-migrate -count=1
$env:BANKING_STORAGE_SOURCE_ENV=(Resolve-Path ../../deploy/banking-postgres/finance.mysql-acceptance-test.env.local).Path
$env:BANKING_STORAGE_TARGET_ENV=(Resolve-Path ../../deploy/banking-postgres/finance.acceptance-test.env.local).Path
$env:BANKING_STORAGE_ROLLBACK_ENV=(Resolve-Path ../../deploy/banking-postgres/finance.mysql-rollback-test.env.local).Path
go test -buildvcs=false -tags=integration ./internal/storage -count=1 -v
```

Integration fixtures must already have baseline migrations and storage fencing. Their database names must contain `test` and either `p7` or `finance`. The test refuses existing business rows and never clears a database. Provision fresh isolated schemas/configuration for each full run; the acceptance files above identify schemas already populated by this P7 acceptance and cannot be blindly rerun. The suite creates explicitly simulated accounts and a transfer through the real execution gateway, preserves rejected and legitimately expired/unconsumed authorizations, tests controlled copy rollback, compares the complete snapshot, verifies receipt-bound no-write replay and receipt mismatch refusal, replays a migrated authorization without reposting, rejects stale rollback after new audit rows, and reverse-imports into the fresh third database. Missing environment paths produce a skip, not evidence of a successful database run.
