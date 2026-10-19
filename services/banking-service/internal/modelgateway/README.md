# Controlled model gateway

`New(Config, Budget)` exposes authenticated `POST /v1/chat/completions` and
`GET /v1/models`. One gateway credential binds one configured project and policy;
caller project/user fields cannot override that binding. `GET /healthz` and
`GET /readyz` contain no configuration details. Authenticated model metadata binds
deployment verification to the project, policy hash and maximum output tokens.

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
