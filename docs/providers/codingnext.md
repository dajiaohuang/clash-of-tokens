# Coding next provider adapters

`internal/providers/codingnext` provides native Go clients for the pinned
OmniRoute contracts `freebuff`, `codebuddy-cn`, and `zed-hosted`. The package is registered in the gateway; configure a source and your own credential:

```go
client := codingnext.New(source)
defer client.Close()
response, err := client.Do(ctx, "chat", model, stream, body, headers)
```

Only the Chat Completions protocol is accepted. The adapters read the token
named by `source.key_env`, never read product credential files, never launch a
subprocess, never follow redirects, and never forward caller authentication or
cookie headers. `X-COT-Session` is an optional opaque conversation key. It is
used only to serialize and continue an adapter-owned session.

## Adapter matrix

| Adapter | Default route | Authentication | Wire behavior |
| --- | --- | --- | --- |
| `freebuff` | `https://www.codebuff.com/api/v1` | `Authorization: Bearer $key_env` | Acquires `/freebuff/session`, starts `/agent-runs`, then calls `/chat/completions`; model aliases select the pinned `base2-free-*` agent and the Buffy system prompt is inserted when absent. |
| `codebuddy-cn` | `https://copilot.tencent.com/v2/chat/completions` | `Authorization: Bearer $key_env` | Sends the pinned CodeBuddy CLI headers and forces upstream `stream: true`; non-stream callers receive a bounded aggregate JSON response. |
| `zed-hosted` | `https://cloud.zed.dev/completions` | `Authorization: Bearer $key_env` | Sends the Zed `{thread_id,prompt_id,provider,model,provider_request}` envelope and converts Zed NDJSON status/events into OpenAI SSE or JSON. |

Freebuff and CodeBuddy preserve the complete JSON request map, including
history and tool definitions. Their provider-required model/stream fields and
Freebuff metadata are the only intentional additions or changes. Zed accepts a
narrow text-only request subset. It rejects tools, multimodal content, response
formats, unknown fields, and provider controls that cannot be represented
without dropping data. Zed model families are inferred as `anthropic`,
`google`, `open_ai`, or `x_ai`; provider-native request conversion is explicit
and does not silently flatten unsupported content.

Successful streaming responses require a real completion marker: Freebuff and
CodeBuddy require an OpenAI `[DONE]` event, while Zed requires `[DONE]`, a
terminal status such as `stream_ended`, or a terminal provider event. An EOF
before that marker is returned as `ErrTruncated`. SSE frames, NDJSON lines, and
buffered bodies are bounded; `io.Pipe` preserves downstream backpressure and
closing the response cancels the upstream body.

## Configuration notes

The configured base URL may be a host/API prefix or the exact route shown in
the matrix. It cannot contain user info, query strings, or fragments. HTTPS is
required except for loopback test endpoints. A credential environment variable
is always required, even when a provider is marked anonymous in another
catalog.

For standalone `zed-hosted`, `key_env` must contain a current Zed LLM bearer
token for `POST /completions`. The product OAuth and `/client/llm_tokens`
organization exchange is intentionally outside this adapter because
`config.Source` carries one explicit secret and no refresh or organization
authority. The adapter does not refresh or invent that token.

## Evidence and limits

The wire behavior was cross-checked against the pinned local snapshot at
`.clash-tokens/reference/omniroute`:

* `open-sse/executors/freebuff.ts` — Freebuff session, agent-run, metadata,
  agent mapping, and completion routes.
* `open-sse/executors/codebuddy-cn.ts` and the CodeBuddy registry — forced
  streaming, opt-in `reasoning_summary`, CLI headers, and endpoint.
* `open-sse/executors/zed-hosted.ts` and `open-sse/shared/zedAuth.ts` — Zed
  completion envelope, provider wire names, NDJSON/status framing, and the
  distinction between user auth and the short-lived LLM token.

The package tests use local `httptest` servers to verify credential isolation,
request shape, session/run lifecycle, streaming aggregation, Zed envelope
translation, truncation rejection, and unsupported-semantics rejection. They do
not claim that these private upstream services are currently available or that
their contracts will remain stable.
