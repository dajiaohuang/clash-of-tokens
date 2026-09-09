# Website HTTP adapters

The `internal/providers/webhttp` package independently implements the documented wire behavior of these reference adapters. It does not launch a proxy service or read credentials from the reference repositories.

| Adapter | Endpoint | Credential | Conversation / delivery |
|---|---|---|---|
| `venice-web` | `https://venice.ai/api/chat` | Explicit session Cookie in `KeyEnv` | Full text message history; SSE |
| `inkeep` | `https://api.inkeep.com/v1/chat/completions` | Explicit site-bound token in `KeyEnv` | Full text message history; SSE |
| `gptanon` | `https://www.gptanon.com/api/chat/stream` | Explicit `anonymous: true` | Single user prompt; token/complete/done SSE |
| `perfectassistant` | `https://perfectassistant.ai/ai/free` | Explicit `anonymous: true` | Single user prompt; buffered JSON, optional one-chunk SSE delivery |

All accept `chat` only. Tools, vision and unsupported parameters are rejected. Perfect Assistant targets are upstream tool IDs; the requested ID is preserved, without the reference's fallback to `brainstorm-tool`. It uses the reference's professional tone and Chinese language fields. There is no fabricated token accounting or typing delay. GPTAnon and Perfect Assistant reject history instead of silently dropping it. An upstream error/truncated SSE never becomes a fabricated answer. A public endpoint may no longer allow anonymous requests; such requests fail normally without account creation or quota rotation.

Only local contract tests have run for these four sources; no live availability claim is made. `go test ./internal/providers/webhttp` covers the reference request mappings, streaming/JSON output, credential isolation, unsupported semantics, truncation, correction conflicts and cancellation.

Reference provenance:

- OmniRoute, MIT, commit `ba597b631d22d85e56db6982f24b7d1ebe238df9`, `open-sse/executors/venice-web.ts`.
- lza6/inkeep-2api, Apache-2.0, commit `65677dd24d7aa5e1354e7de0b9e2ceadc2aa9ab7`, `app/providers/inkeep_provider.py`.
- lza6/gptanon-2api-cfwork, Apache-2.0, commit `b6887053253d151858863e48f0ce1289d0a5b221`, `worker.js`.
- lza6/perfectassistant-2api-cfwork, Apache-2.0, commit `13ded16dfae6fdf95597d6dfd6cd977a12386d49`, `worker.js`.

Original licenses are retained in `docs/licenses/`. Protocol constants and wire shapes are attributed above; the Go client, validation, parsers and tests were authored for this project.
# AI Free Forever

## ToolBaz credential-only submission

`toolbaz` posts to `https://data.toolbaz.com/writing.php`. `KeyEnv` must contain
`{"session_id":"YOUR_SESSION","token":"YOUR_EXISTING_WRITING_TOKEN"}`. The token
must already have been issued for that session by the service. No token endpoint,
browser stealth, verification challenge, account creation or quota refresh is
performed. An expired token fails and must be replaced by the user.

The adapter accepts one user text prompt. The response is buffered, capped at
1 MiB, and sent as one content delta for streaming requests; it does not simulate
typing. Its direct adapter response carries `X-COT-Delivery: buffered`. HTML line
breaks/entities are decoded while surrounding whitespace is preserved. Local
tests verify a single authorized submission and no token-generation request.
There is no live validation.

Reference: [lza6/toolbaz-2api-docker](https://github.com/lza6/toolbaz-2api-docker/tree/4aa7f875cde50f6741af79d0b9a4fc5a4811f6ba),
`app/providers/toolbaz_provider.py`. Browser automation in that reference was
not migrated; only its writing request/response contract is used.

`aifreeforever` uses native HTTP at `https://chat.aifreeforever.com/api/chat`.
`KeyEnv` supplies an explicit Cookie header; no browser profile is read. The
payload preserves user/assistant text history as AI SDK message parts, with
`modelId` set to the configured upstream model. System, tools, images and
unsupported parameters are rejected. AI SDK `text-delta` records are streamed;
`finish` or `[DONE]` is required for success. Unknown tool/reasoning output
events fail instead of being presented as answer text.

The reference uses browser-context fetch, while this implementation uses HTTP.
Deployments requiring browser verification can therefore reject it; no live
availability is claimed. Local tests cover history preservation, whitespace,
authentication, missing completion and error records.

Reference: [lza6/Aifreeforever-2api](https://github.com/lza6/Aifreeforever-2api/tree/b9bf20a2a77e419d40afed091e77f9202f6726cb),
`app/providers/aifreeforever_provider.py`, Apache-2.0. The shared lza6 license
notice is retained under `docs/licenses`.
