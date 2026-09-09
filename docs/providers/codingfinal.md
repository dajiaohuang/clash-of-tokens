# Coding final adapters

`internal/providers/codingfinal` is an opt-in package for the strict93 coding
sources whose pinned OmniRoute references contain a direct wire protocol.
Callers construct it with an explicit `config.Source`; authentication is read
from that source's `KeyEnv` for each request. Caller cookies, authorization
headers, and other credential material are never copied upstream.

The package implements:

- `cursor`: Cursor `agent.v1.AgentService/Run` over Connect framing and the
  hand-encoded protobuf request/response fields pinned in
  `reference/omniroute/open-sse/utils/cursorAgentProtobuf.ts`.
- `windsurf` (also named `devin-desktop` in the pinned registry): the Devin Desktop compatibility transport,
  `exa.auth_pb.AuthService/GetUserJwt` followed by
  `exa.api_server_pb.ApiServerService/GetChatMessage`, using the pinned
  Connect/protobuf field tables in `reference/omniroute/open-sse/executors/devin-desktop.ts`.
- `qoder`: `pt-*` personal access tokens use the official `qodercli` companion with tools disabled, explicit model mapping, restricted environment, bounded execution, and strict completed-result parsing. Other tokens use the DashScope-compatible fallback. The PAT path is registered and tested with a local companion fixture; no live account is claimed.
- `trae`: Trae/SOLO's remote `chat_sessions` HTTP session creation followed by
  its event SSE stream. The adapter supplies only the explicit JWT from
  `Source.KeyEnv`; optional `Source.AccountIDEnv` is used as the documented
  `web_id` value.
- `v0-web`: the pinned v0 web `POST /api/chat` JSON/SSE flow, authenticated by
  the explicit cookie value in `Source.KeyEnv`. An explicitly configured
  `/v1/chat/completions` endpoint is also accepted for installations using the
  registry's compatible URL.
- `warp`: Warp's `warp.multi_agent.v1.Request` protobuf over
  `POST https://app.warp.dev/ai/multi-agent`, with the source JWT and the
  protobuf response event stream translated to OpenAI SSE. The strict direct
  path accepts one pinned model and one user message per request; it does not
  claim unproven remote conversation continuation.
- `zcode`: the documented local ZCode app-server companion over stdio. The
  adapter performs the JSON hello/ack handshake, then the 13-byte framed
  SocketProtocol RPC (`initialize`, `createSession`, `setModel`, `sendPrompt`,
  `readSession`, and `closeSession`). ZCode owns authentication in its local
  `builtin:zai-coding-plan` profile; no credential is read or forwarded by the
  gateway. Completed turns are buffered into OpenAI JSON or SSE responses.

Cursor request text follows the pinned `flattenMessages` helper exactly:
one plain user turn is sent as its raw text; system messages are collected in
their original order and prepended with two newlines; every other turn is
labelled `User:`, `Assistant:`, `<role>:`, or `Tool result (<id>):`, with
assistant tool calls emitted as `Assistant called tool <name> (<id>) with
arguments: <json>`. The Go adapter rejects tool execution/history because it
does not implement the required round trip, while preserving labels for the
remaining multi-turn text. Cursor and Windsurf gateway `X-COT-Session` values
are rejected: the direct HTTP adapter has no durable upstream stream/history
to make that continuation genuine.

The ZCode process is started only when the caller explicitly selects the
adapter. By default the adapter follows the source's companion layout
(`%USERPROFILE%\\.zcode\\server\\node` plus `zcode-server.cjs`) and otherwise
uses `ZCODE_BIN` with the JSON-array `ZCODE_ARGS` override. It uses direct
`exec.CommandContext` without a shell, drains diagnostics without exposing
them, enforces bounded frames and startup/turn timeouts, and closes the local
session before terminating the process. Set `ZCODE_CWD` and
`ZCODE_PROVIDER_ID` only when the local installation requires them.

All direct adapters accept the gateway's `chat` protocol and text content.
Connect responses require valid bounded frames and explicit source completion
evidence; malformed protobuf, invalid gzip, oversized input/output, upstream
errors, cancellation, and truncated streams are surfaced as errors. Cursor,
Windsurf, Trae, v0 and ZCode are registered in the shared catalog when their
source entries opt in. Warp and ZCode now have registered paths. Qoder PAT companion is registered after final validation.

The ZCode implementation is checked against the pinned source files
`.clash-tokens/reference/omniroute/open-sse/executors/zcode.ts`,
`.clash-tokens/reference/omniroute/open-sse/executors/zcodeProtocol.ts`, and
`tests/fixtures/fake-zcode-app-server.mjs`, which define the process selection,
model allow-list, handshake, frame values, RPC method sequence, and completed
assistant message shape.

Warp schema verification uses Xchat1/Warp2Api commit `c3a7fb35ae8c34cd8246cb881ffc5852601e49ff`, `proto/request.proto`, `proto/response.proto`, and `proto/task.proto`. Request input follows fields `2/6/1/1/1`; settings model follows `3/1/1`. Independent response vectors cover AddMessagesToTask and AppendToMessageContent. Quota, unavailable, internal-error, missing finish reason, and unsupported tool execution fail the request.

ZCode requires the explicit operator setting `COT_ZCODE_ALLOW_UNSAFE=1`: its upstream build mode can execute local actions and has no documented no-tools mode. The adapter uses a temporary workspace and restricted child environment; this is not an operating-system sandbox. `ZCODE_CWD` is not passed as an editable workspace.
