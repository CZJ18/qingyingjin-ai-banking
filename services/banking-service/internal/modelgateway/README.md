# Controlled model gateway

`--prepare-execution-plan` fills the manual candidate-JSON bootstrap gap using
the same local manifest/Approval/Policy/instructions validators. It emits only
`CANDIDATE_NO_EXECUTION`, with no confirmation, live quota or external attestation.
It never initializes credential config, SQL, clients or a listener. Dispatch
still rejects even an independently confirmed candidate; only ordinary preflight
can consume one. See `deploy/banking/REAL-MODEL-RUNBOOK.md` for the separate review,
exact-byte confirmation, protected preflight and explicit paid-run consent steps.

Stage5 preparation uses `--offline-preflight --readiness-manifest-file <file>`
with the non-secret `deploy/banking/model-integration-manifest.json.example`.
It never loads `--env-file`, follows evidence references, opens SQL, probes Coze
or dispatches requests, even if scope-probe flags were supplied. Explicit fresh
approval, policy and execution-plan flags use the existing validators. The
explicit `--expected-database` must match the new approval, even offline. The
report separates PASS/FAIL/PARTIAL/BLOCKED/NOT_INSPECTED. External reference
presence never establishes publication, permissions, prices, usage or settlement.
Remaining SQL limits stay null. A declared price bound is labeled as unverified
policy arithmetic, not a proven real maximum cost. Offline reports never emit
READY, start a listener, create confirmation or authorize a call; nonzero exit
is expected even for structurally complete PARTIAL preparation.

All enabled workflow roles must match the exact approval pin set. Explicitly
disabled specialists carry no IDs/hashes; this declaration still does not prove
the runtime disabled them. Never enlarge approval to accommodate an unexplained
manifest. Credentials, PATs and API keys have no fields in this schema; use only
non-secret evidence references and independently approved secret injection.
Actual model/version identity, deployed bindings and raw published Canvas bytes
remain subject to the existing protected runtime checks and independent review.

Ordinary `--preflight` now requires an exact independently confirmed plan before
loading any supplier configuration, even when all plan flags were omitted. To
avoid a snapshot/confirmation bootstrap cycle it alone may inspect a reviewed
`CANDIDATE_NO_EXECUTION` document with the existing approval, instructions and
fingerprint bindings. A candidate never authorizes startup, an HTTP grant or a
model call. Successful runtime checks produce a new READY snapshot; its different
bytes require separate confirmation before dispatch. Neither digest is generated
as consent. Without permission to read real credentials, use offline mode only.
Runtime preflight also parses the explicitly supplied non-secret policy and
validates its approval/model/time/monetary/token/database binding before loading
the supplier environment. A bad or missing policy cannot trigger a secret read.

Stage4 dispatch has a separate execution-confirmation gate. A valid fee
approval or READY preflight is not consent to start the listener. For both real
and explicitly simulated approvals, startup requires `--execution-plan-file`, `--execution-plan-sha256` and
the independently supplied `--confirm-execution-plan-sha256` matching those exact
plan bytes. This is checked before loading supplier credentials. Operators must
show the concrete plan and obtain separate explicit user execution confirmation;
never auto-populate the confirmation from configuration or preflight output.

Real preflight accepts `--acceptance-instructions-file` (see the deliberately
incomplete `deploy/banking/model-acceptance-instructions.json.example`). It binds
the persisted approved task digest, test input description, registered read/advice
tools, expected data scope, verification and failure handling into the JSON plan.
Missing instructions keep preflight BLOCKED. These descriptions are neither
new tool grants nor financial authorization; the original task digest, lease,
whitelist, user/account checks and atomic monetary limits remain authoritative.
After confirmation, startup still rechecks policy, protected Coze scope and all
existing admission conditions; Reserve rechecks SQL limits before each request.
The plan snapshot cannot reserve or guarantee unchanged remaining capacity.
Explicit loopback simulation remains a test-only path and never authorizes a
real provider. Synthetic confirmations in isolated tests are not real execution
consent. Every HTTP admission/dispatch request rechecks the exact plan bytes,
since a host can change the source of a read-only bind mount. Drift returns
`MODEL_EXECUTION_CONFIRMATION_REQUIRED` (503), also preserved by the Go Coze
bridge, owner-bound snapshot and Python task failure. Already admitted in-flight
requests are not retroactively revoked; unknown outcomes keep their holds.

Stage3 runtime acceptance additionally supports an explicit `simulation:true`
approval only with a loopback HTTP `/v1` supplier, a `synthetic-` model, a
`synthetic:` price source, an exact `_test` database and `BANKING_ENVIRONMENT=test`.
Development and production reject that approval. The matching approval ledger is
still mandatory in this mode: no request/concurrency/money checks are skipped.
Responses carry `X-Banking-Simulation`; preflight labels synthetic scope and
permission evidence as test fixtures, never as a real publication or supplier bill.

Authenticated `GET /api/v1/model-admission` forwards the existing gateway's
read-only `/v1/budget` owner-bound snapshot to Vue. It exposes caps and remaining
approval amounts/requests/concurrency, never the gateway credential, task digest,
workflow capability or a financial execution authorization. The user must match
the approval owner. The snapshot cannot authorize a task or bypass atomic Reserve.

2026-10-21: paid startup now additionally requires an explicitly named NEW
`--approval-file`, independently confirmed `--approval-sha256` and `--run-id`.
No historical approvals are discovered. The same command supports `--preflight`:
it emits JSON and exits nonzero on blockers without starting a listener,
minting a grant, reserving money or calling a supplier. A missing/invalid approval
stops before the credential environment is loaded.

The approval binds provider/model, project/database, currency/amount, reviewed
price-policy hash, authenticated user, persisted task request digest (including
its account bindings), workflow ID/Canvas digest, effective window, request count,
concurrency and token limits. See `deploy/banking/model-call-approval.json.example`;
its empty fields deliberately do not authorize anything.

Planning and specialist explanations may use different workflows. Only workflows
explicitly listed in `additional_workflows` (ID to reviewed Canvas SHA256; at most
31 entries) join the primary workflow. They share the same aggregate money,
request and concurrency limits; configured workflow pins alone confer no approval.
For multiple workflows, `--published-canvas-file` is a strict JSON object whose
exact keys are all approved workflow IDs and whose string values preserve each
exported Canvas's exact bytes. Each digest and live scope pin must match. A single
workflow continues to use the raw exported Canvas file.

`Reserve` enforces approval caps under the existing project SQL row lock. New
immutable `approval_binding` events bind an approval ID to its exact digest;
reserve events carry that digest and the full approval in their audit snapshot.
No schema migration or historical row rewrite is needed. All attempts consume
the request cap; reserved/unknown outcomes retain concurrency. Conservative
approval exposure counts each original maximum reservation even after settlement.
Idempotent cached responses consume no new request. Changing an already used
approval ID to increase caps conflicts instead of resetting counters.

Startup/preflight also require new operator permission-review evidence bound to
the actual supplier key fingerprint, approval digest, provider, model, permission
`chat.completions`, and the approval expiration. The review file and separate
evidence bytes are explicitly supplied through `--permission-review-file`,
`--permission-review-sha256`, `--permission-evidence-file`; they are never generated
from a successful fixture. This is operator-reviewed evidence, not a live provider
permission probe or proof that a credential has not since been revoked.

`--allow-runtime-scope-probe` explicitly permits one bounded authenticated GET to
the supplied Coze origin. `--coze-scope-env-file` contains only
`BANKING_COZE_BASE_URL` and `BANKING_COZE_PAT`. Reviewed exported Canvas bytes
(`--published-canvas-file`) must match the approval and workflow pins. Live scope
must match the reviewed `--gateway-origin`, model and output cap. Preflight checks
configuration scope and exported graph; the existing Coze execution path still
checks the actual stored published graph before a model request. No metadata-only
check is represented as proof of an entire remote deployment.

Permission review fields: `approval_sha256` (canonical Approval.Fingerprint),
`credential_sha256` (SHA256 of the exact supplier key), `provider_origin`, `model`,
`permission`, `evidence_sha256` (SHA256 of separately reviewed evidence bytes),
`valid_until` (same instant as approval expiration). CLI review SHA is the SHA256
of the exact review file bytes. Approval CLI SHA likewise hashes exact file bytes.

Preflight reports maximum next-request risk and remaining approval/project/user
money, requests and concurrent slots as a read-only snapshot. READY is not an
authorization or reservation: actual dispatch revalidates scope and atomically
reserves again. Known refusal codes propagate as `MODEL_APPROVAL_REQUIRED` (403)
and `MODEL_APPROVAL_LIMIT_EXCEEDED` (429) through Go and Python. This does not add
automatic retries or change financial prepare/authorize/execute.

Completion responses expose a non-secret `X-Model-Request-Fingerprint` and, once
the reservation exists, `X-Model-Reservation-ID`. Cached replays retain the same
reservation ID. `X-Model-Accounting-State` distinguishes an unverified hold,
reported/unsettled usage and independently settled records. Raw request keys,
credentials and supplier-reported cost are not reflected as settlement proof.

2026-10-20: reviewed workflows additionally use `/v1/workflow-grants` and
`/v1/workflow-admit`. The opaque capability binds the authenticated owner, live
SQL task version/lease/digest, exact Canvas/input digests and reviewed price/bounds.
It cannot override request keys, enable fallback/tools, or settle supplier charges.
See the [current evidence and remaining deployment blockers](../../../../docs/ai-closure-20261020.md).

`New(Config, Budget)` exposes authenticated `POST /v1/chat/completions` and
`GET /v1/models`. One gateway credential binds one configured project and policy;
caller project/user fields cannot override that binding. `GET /healthz` and
`GET /readyz` contain no configuration details. Authenticated model metadata binds
deployment verification to the project, policy hash and maximum output tokens.

Schema021 additionally requires authenticated Banking identity for every paid
POST and `GET /v1/budget`, using `X-Banking-Authorization`. The fixed Banking
origin revalidates token, owner and active status; caller user IDs are ignored.
Both project and user monthly monetary holds commit atomically before dispatch.
See [monthly scope and current Coze blocking boundary](../../MODEL-USER-MONTHLY.md).

The supplier origin is fixed to `https://api.deepseek.com/v1` in production and
development. Tests may explicitly substitute a loopback origin with
`outbound.TestPolicy()`. TLS certificate checks stay enabled, redirects are
rejected, and the gateway performs no automatic HTTP retries. The supplier key
replaces the incoming gateway credential and is never forwarded to the client.

Accepted requests contain one configured model, explicit `max_tokens` within the
policy maximum, text messages, bounded sampling/penalty/stop options, text or JSON
response format, and optional function tool schemas/history. Function definitions
are model input; this endpoint never executes tools. SDK tool-call indices and
`tool_choice` are supported. Images, files, built-in search/paid tools, prefix
completion, thinking switches, logprobs and other unrecognized fields are rejected
before reservation. Every JSON envelope uses an exact field allowlist, rejects
duplicate keys and trailing documents, and has a request limit of 1 MiB.

Each paid attempt reserves before HTTP dispatch. Without an explicit bounded
`X-Model-Request-ID`, each request gets an independent random key. An exact replay
can return a protected project/policy response cache; changed bytes conflict and
an existing unresolved attempt is never dispatched again. The budget service
keeps the full conservative reservation until independent settlement.

Responses are limited to 2 MiB. Missing or malformed token usage is unknown, never
fabricated zero; explicitly present zero counts are valid. Supplier errors,
redirects, TLS failures, cancellation, timeout and incomplete output keep the
reservation. Valid usage exceeding the bound, or a wrong model identity, reaches
the ledger for pool freezing. Recording uses a bounded context independent of
client cancellation. Raw upstream error bodies and internal errors stay private.

Streaming is **buffered**, not delivered in real time. The gateway overrides the
pinned Eino SDK's `include_usage:false` to `true`, then requires consistent response
IDs, valid chunks, terminal finish reason, final usage and `[DONE]` before
recording and returning any bytes. Unfinished streams retain their reservations.

Tests use local HTTP/TLS fake suppliers and synthetic prices. They do not prove
live Coze workflow compatibility, real supplier price bounds, removal of old keys,
absence of deployment bypasses, per-user cost attribution, or real bill matching.
