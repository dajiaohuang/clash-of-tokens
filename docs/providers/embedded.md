# Embedded web providers

`internal/providers/embedded` contains independent Go implementations of
three web-backed protocols. The package has the same lifecycle shape as the
native HTTP client:

```go
c := embedded.New(source)
defer c.Close()
resp, err := c.Do(ctx, protocol, model, stream, body, clientHeaders)
```

`Do` accepts the gateway's `chat` protocol and returns an OpenAI-compatible
JSON response or a lazily converted `text/event-stream`. It uses only the
credential environment variables named by `Source.KeyEnv` and, for Notion,
`Source.AccountIDEnv`. Incoming caller headers are never copied to the web
request. Redirects are disabled, HTTPS is required away from loopback, and a
cancelled context is passed through to the provider request and stream body.

## Source mappings

| `Source.Adapter` | `KeyEnv` | `AccountIDEnv` | `Project` | Web protocol |
| --- | --- | --- | --- | --- |
| `fanzha` | Access token | unused | unused | `POST /api/ai/create_session`, then `POST /api/ai/chat?type=0` SSE |
| `tabbit` | Full Tabbit cookie | unused | Tabbit version, for example `1.1.39(10101039)` | Fresh `/panel/session`, then signed `/api/v2/chat/completion` SSE; bounded `/api/v1` compatibility retry |
| `notion-web` | `token_v2` value or full Cookie header | Notion user ID | Notion space ID | `POST /api/v3/saveTransactionsFanout`, then `POST /api/v3/runInferenceTranscript` NDJSON |

`BaseURL` overrides the public web host and is useful for a loopback
`httptest.Server`. Empty values use the reference host for that adapter. The
shared configuration registers `fanzha` and `tabbit`. Notion remains an
unregistered experimental implementation pending stronger transcript parsing
and completion evidence.

The fanzha implementation deliberately does not read or refresh a refresh
token: `config.Source` has no dedicated refresh-token field, and treating
`AccountIDEnv` as one would make the configuration misleading. Supply a
current access token through `KeyEnv`.

## Protocol behavior

The fanzha adapter accepts exactly one user message (including text parts),
creates a server session, and sends the resulting session ID with the query.
Provider `data` events whose nested type is `answer` become OpenAI deltas.
Non-streaming calls aggregate the provider SSE before returning one JSON
completion. Missing or malformed session responses and truncated SSE records
are errors. It currently requires an explicit `[DONE]` marker; streams that
terminate without one fail closed. This requirement still needs live validation.

Tabbit credentials are sent as a Cookie. The adapter creates a fresh session
for every request, initializes its page, builds the complete browser-like
request payload, and signs the exact JSON bytes with:

```text
x-nonce = HMAC-SHA256(sign_key,
  x-timestamp + "." + x-signature + "." + SHA256(body))
```

It sends `x-req-ctx` as base64 of `Project`, and generates the `unique-uuid`
browser marker used by the reference. The pinned reference implementations
use `/api/v1/chat/completion`; the reverse-engineering document records v2 as
the current endpoint. This provider tries v2 first and retries v1 only for a
404/405 response. A 499 response refreshes the sign key from
`GET /chat/sign-key` once. No CDP browser, cookie export, check-in, or account
automation is started by this package.

Notion credentials are converted to the exact web header form expected by the
reference (`token_v2=...` when a bare value is supplied). The adapter creates
a `workflow` thread for the ordinary mapped models and a `markdown-chat`
thread for mapped `vertex-*` models. It parses `markdown-chat`, patch, and
record-map NDJSON records into OpenAI deltas. Incremental patches are emitted
as they arrive; a later full record is used only when no incremental patch
was seen. `stream=false` aggregates the same converted stream.

Fanzha and Tabbit reject history, system messages, tools and images explicitly;
they support a single user text turn. Stream lines/events are capped at 1 MiB
and aggregated non-streaming responses at 16 MiB. Token usage is omitted
because the protocols do not supply reliable token counts.

All three adapters reject protocols other than `chat`. The package does not
claim live account access, model availability, quota eligibility, or stable
web behavior. The tests use local `httptest` fixtures only; they do not make
real generation requests, register accounts, solve challenges, or bypass
verification.

## Reference provenance and attribution

Only protocol behavior was reimplemented in Go. No source file was copied
into the provider package.

| Repository and pinned commit | Protocol paths read | License evidence at the pinned commit |
| --- | --- | --- |
| [lfzk550/fanzha-ai-proxy](https://github.com/lfzk550/fanzha-ai-proxy/tree/3ebb42813915bcd256894786637ef6568d3713e9), `3ebb42813915bcd256894786637ef6568d3713e9` | `main.py` (`/api/ai/create_session`, `/api/ai/chat?type=0`, answer event shape), `README.md` | No license file or license grant was present in the checked-out commit; this implementation is an independent protocol description and retains attribution. |
| [goehou/tabbit-toy](https://github.com/goehou/tabbit-toy/tree/9b26b512fd1c9961a641d64acf3551f7001b6091), `9b26b512fd1c9961a641d64acf3551f7001b6091` | `src/server.mjs`, `src/config.mjs`, `scripts/lib/tabbit.mjs`, `docs/逆向流程与协议.md` | No license file was present; the README describes personal research and includes a non-affiliation/disclaimer. |
| [hoinata/tabbit2api](https://github.com/hoinata/tabbit2api/tree/2f1b5ac65df7ae43c591df1e91f3e10a931e92b1), `2f1b5ac65df7ae43c591df1e91f3e10a931e92b1` | `routes/openai_compat.py`, `core/tabbit_client.py`, `README.md` | README states MIT License; no separate license file was present. The Go code is independently authored. |
| [lza6/notion-2api](https://github.com/lza6/notion-2api/tree/2581198765ffc1f3acd4402e39f574898cee3b64), `2581198765ffc1f3acd4402e39f574898cee3b64` | `app/providers/notion_provider.py`, `app/providers/base_provider.py`, `app/core/config.py`, `LICENSE` | Apache License 2.0 (`LICENSE`). Only protocol facts are used here; no Python code is copied. |

The public reference checkouts are kept under
`.clash-tokens/reference/embedded/` for auditability. They are not runtime
dependencies and must never be populated with local account credentials.
