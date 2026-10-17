# Migration 012: phone/password credentials

The normal `cmd/migrate` command applies baselines 001–010, storage fence 011,
then this additive migration. `migration.CurrentVersion` is now 12. Do not edit
historical migration files or their stored checksums.

Only `banking_users` changes:

| Column | SQL type | Null/default | Purpose |
| --- | --- | --- | --- |
| `phone_number` | `VARCHAR(20)` | nullable | Server-normalized phone identity; existing users stay unbound |
| `password_hash` | `VARCHAR(255)` | not null, `''` | Encoded salted password hash; never a plaintext password |
| `auth_version` | `BIGINT` | not null, `0` | Revocable authenticated session version |
| `refresh_hash` | `VARCHAR(64)` | nullable | SHA-256 of random refresh credential |
| `refresh_expires_at` | `BIGINT` | not null, `0` | Expiration timestamp managed by authentication service |

Both dialects enforce unique `phone_number` and unique `refresh_hash`, while
allowing multiple `NULL` values. Existing user IDs, finances, rows, and the 46
business table count are preserved. Public user response entities are unchanged;
the authentication service owns credential reads/writes.

PostgreSQL uses a transaction-scoped advisory lock, re-reads migration metadata
after acquiring it, and commits DDL and version/checksum together. Any failed
statement rolls back all five columns and both indexes.

MySQL uses a dedicated connection and named lock. Because DDL auto-commits, each
column and unique index is inspected before skipping a previously committed
step. Incompatible type, signedness, nullability, default, indexed column or
prefix length stops migration instead of silently adopting an unsafe schema.
Version 12 is recorded only after every DDL step succeeds.

For either dialect an applied 012 checksum mismatch stops startup/migration.
Back up and use the existing reviewed migration/cutover procedure. A finance
snapshot after 012 must be imported into an equally upgraded 012 target; the
historical frozen 011 MySQL instance cannot become writable as a fallback.

## Snapshot/cutover compatibility

The storage allowlist remains the same 46 table names. Its `columns()` function
reads `information_schema.columns` in column-name order at runtime; it has no
hardcoded list of `banking_users` fields. All five new columns are therefore
included automatically in canonical row hashing, copy parameters and comparison.
Their SQL types are already supported by the existing `string` and `integer`
normalization rules. Nullable phone and refresh hash remain explicit NULL cells.

`compareColumnContracts()` runs before copy or comparison. A 012 source and an
011 destination differ in the `banking_users` column contract and are refused;
no credential column can be silently dropped. Provision an independent empty
012 target for a future reverse migration. Historical P7 receipts describe the
schema and snapshot used at that time and must not be edited to imply they
covered these new credentials. No storage engine or manifest code change is
required for this additive migration.

Run the isolated live acceptance tests from `services/banking-service`:

```powershell
$env:BANKING_AUTH_MIGRATION_POSTGRES_ENV = 'D:\智慧农业\deploy\banking-postgres\finance.core-test.env.local'
$env:BANKING_AUTH_MIGRATION_MYSQL_ENV = 'D:\智慧农业\deploy\banking-postgres\finance.mysql-core-test.env.local'
go test -buildvcs=false -tags integration ./internal/migration -run '^TestPhoneAuth' -count=1 -v
```

The test configurations must explicitly name `_test` databases. PostgreSQL tests
create/remove random schemas. MySQL tests add 012 to the existing isolated core
test database, preserve historical records, roll back user fixtures, and use a
separate `auth012_fixture_*` table for interruption/retry checks. Neither test
opens the main finance database or historical frozen authority.
