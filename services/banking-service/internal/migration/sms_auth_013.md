# Schema 013: phone verification and password recovery metadata

Migration 013 extends `banking_users` only. It does not create business tables,
change balances, claim existing phone numbers as verified, or replace credentials.
Historical SQL bytes and checksums for 001–012 remain unchanged. The existing
46 financial business tables and storage-fence manifest remain the same.

| Column | SQL type | Default | Meaning |
| --- | --- | --- | --- |
| `phone_verified_at` | signed `BIGINT NOT NULL` | `0` | Unix milliseconds when the current phone proof was accepted; zero means no proof. |
| `password_changed_at` | signed `BIGINT NOT NULL` | `0` | Unix milliseconds of an accepted password change; zero for pre-013 users. |
| `phone_verification_method` | `VARCHAR(16) NOT NULL` | `''` | Empty means unverified. `development` denotes development inbox proof, and `tencent` denotes actual Tencent SMS proof. |

The method is persisted explicitly so a development inbox challenge cannot later
be used as proof of real phone ownership after changing the configured provider.
The application must check the method against its active provider for recovery.
This migration does not itself send SMS or expose these fields through an API.

`migration.Apply` runs the unchanged baseline, storage fence 011, phone/password
012, then 013. `CurrentVersion` is 13. PostgreSQL takes advisory transaction lock
77113813 and atomically commits all columns and the migration checksum. A late
DDL failure rolls everything back. MySQL uses dedicated connection named lock
`banking_sms_auth_013`. Its DDL is individually committed; retry validates every
already-present column's type, signedness, nullability, default, length and
generated/extra flags before skipping it. Both dialects reject changed checksums.

The old MySQL source at schema 011 remains frozen. It must not be upgraded merely
to run these tests. Production migration is performed separately by an operator
using the existing guarded migration process; these tests do not migrate it.

Actual verification on 2026-10-16:

- `go test ./internal/migration -count=1`: passed.
- `go test -tags=integration ./internal/migration -run 'TestSMSAuth(Postgres|MySQL)Live' -count=1 -v`: six leaf cases passed, no skips.
- PostgreSQL used a random schema within the explicitly configured `_test`
  database, which was removed after testing. MySQL used the existing isolated
  `*_core_test` database and non-`banking_` temporary DDL fixtures. Test rows were
  rolled back and temporary tables removed.
- Coverage includes existing credential preservation, defaults, millisecond
  precision, concurrent/repeated application, historical checksums, no new
  tables, PostgreSQL rollback, MySQL interrupted DDL recovery, incompatible
  column rejection, and changed migration checksum rejection.

Evidence lives in `output/sms-auth/migration-contract-red.txt`,
`output/sms-auth/migration-unit.txt`, and `output/sms-auth/migration-live.txt`.
The migration test fixture loads private env files internally and refuses any
database name that does not end with `_test`; it never prints configuration.
