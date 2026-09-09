# Coding more provider adapters

`internal/providers/codingmore` contains native adapters for Amazon Q Developer,
Augment, and the official Devin CLI companion. They are opt-in and are not
added to the shared provider registry
by this package:

```go
client := codingmore.New(source)
defer client.Close()
response, err := client.Do(ctx, "chat", model, stream, body, headers)
```

The Amazon Q adapter accepts OpenAI Chat Completions text requests and sends
the request to AWS CodeWhisperer's `POST /generateAssistantResponse` route.
It converts the AWS EventStream response into OpenAI SSE or JSON, checks both
EventStream CRCs, enforces bounded frames and total response bytes, and
requires a real completion marker (`messageStopEvent` or a completed assistant
event). Calls using one `X-COT-Session` are serialized with a context-aware
`providerutil.Gate` so cancellation does not leave a turn holding the lock.

Authentication comes only from the environment variable named by
`source.key_env`. The adapter creates its own bearer and AWS service headers;
caller cookies, authorization headers, and other credential material are not
forwarded. `source.base_url` must be an explicit HTTPS host or endpoint, with
HTTP accepted only for loopback test servers. If `source.account_id_env` is
set, its value is sent as the optional CodeWhisperer `profileArn` field. The
adapter does not inspect AWS/Kiro credential files, refresh tokens, or create
anonymous sessions.

The Amazon Q request surface is intentionally narrow: protocol `chat`, text
messages with `system`, `user`, and `assistant` roles, and the standard
`max_tokens`, `max_completion_tokens`, `temperature`, and `top_p` controls.
The Augment adapter accepts only alternating text `user` and `assistant`
turns. Tool calls, images, multimodal content, `messages` protocol requests,
response-format controls, and other unknown fields are rejected instead of
being flattened or silently dropped. Reasoning event text is preserved in the
gateway response when Amazon Q emits it, but no reasoning control is claimed.

The Augment adapter accepts text-only OpenAI Chat Completions requests and
translates them to Augment's tenant `POST /chat-stream` contract. The native
request carries `chat_history`, `message`, `mode`, `blobs`, and the product
feature flags; each response line is an NDJSON object with `text` and `done`.
Model names ending in `-agent` are rejected because the native agent mode
requires Augment's built-in tool definitions and those tool semantics are not
part of this adapter. Only default uses the documented `CHAT` mode.

The `devin-cli` adapter launches the directly configured `devin` executable
with `acp --agent-type summarizer` and speaks ACP JSON-RPC 2.0 over NDJSON
stdio. It sends the pinned `initialize`, `session/new` (including `cwd`, an
empty `mcpServers` array, and the selected model), and `session/prompt` calls.
Nested `agent_message_chunk` updates and the unary prompt result are converted
to OpenAI SSE or JSON. The summarizer agent is the source's text-only mode;
ACP updates that request file or command tools are not exposed as gateway tool
calls. The subprocess is spawned without a shell, with bounded NDJSON output,
and is terminated on completion, cancellation, or malformed protocol data.

Authentication is optional. When `source.key_env` contains a value, the
adapter passes that value only to the child as `WINDSURF_API_KEY`; otherwise
the Devin CLI may use its own local `devin auth login` profile. It does not
forward caller headers, and `X-COT-Session` is rejected because each request
creates a fresh ACP session. Set `CLI_DEVIN_BIN` to an absolute executable path
when PATH discovery is insufficient; the installer paths from the pinned
reference are checked before the PATH fallback.

## Evidence

The implementation was checked against
[OmniRoute ba597b631d22d85e56db6982f24b7d1ebe238df9](https://github.com/diegosouzapw/OmniRoute/tree/ba597b631d22d85e56db6982f24b7d1ebe238df9)
(MIT) and
[augment2api 6488d6e61228b7520e34d9595a08fcda4b13cad7](https://github.com/linqiu919/augment2api/tree/6488d6e61228b7520e34d9595a08fcda4b13cad7)
(license undeclared; no standalone license file or GitHub license metadata was
present, so this snapshot is provenance only and is not copied). `open-sse/executors/index.ts` explicitly maps `amazon-q` to the Kiro
executor; `tests/unit/repro-9550-amazon-q-alias-resolution.test.ts` verifies
that mapping. The source maintains separate product authentication entries,
while the underlying CodeWhisperer protocol is shared.

The system-role conversion uses a textual `<system-reminder>` prefix because
this product request has no separate system field. This is not a distinct
native system-instruction channel. Configure the AWS region's matching host
when using a region-bound token or profile ARN. No automatic region selection
or cross-host retry is performed.

| Snapshot | Relevant evidence |
| --- | --- |
| `.clash-tokens/reference/omniroute/open-sse/executors/kiro.ts` | Amazon Q/Kiro endpoint, request allow-list, authentication headers, and EventStream event names |
| `.clash-tokens/reference/omniroute/open-sse/executors/kiro/eventstream.ts` | AWS EventStream framing and header encoding |
| `.clash-tokens/reference/omniroute/open-sse/config/providerHeaderProfiles.ts` | CodeWhisperer service headers and streaming target |
| `.clash-tokens/reference/omniroute/open-sse/executors/devin-cli.ts` | Devin CLI is an ACP stdio subprocess integration |
| `.clash-tokens/reference/coding/augment2api/api/handler.go` | Augment tenant `chat-stream` URL, request schema, headers, model mode selection, and NDJSON response shape |
| `.clash-tokens/reference/coding/augment2api/config/config.go` | Augment client user-agent and tenant/token configuration conventions |

The package tests cover request and credential isolation, request translation,
streaming and non-streaming output, usage extraction, session cancellation,
CRC failures, truncation, and unsupported adapters. They use `httptest`; no
live account or quota claim is made.

Augment is registered in the gateway. Initialize with -provider augment -model default -base-url followed by the actual tenant URL paired with your token. No default tenant is invented. The CHAT protocol has no model-selection field; versioned model aliases are rejected.

Devin CLI is registered as a local companion. Initialize with
`-provider devin-cli -model <the Devin model id>`; its `base_url` remains the
virtual `devin://acp/stdio` value and no gateway credential is required when
the local Devin profile is already authenticated.
