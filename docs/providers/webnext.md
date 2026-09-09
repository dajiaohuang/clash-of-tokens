# Long-tail web providers

`internal/providers/webnext` contains native Go clients for the three bounded
long-tail sources in the current inventory:

| Inventory | Adapter | Protocol | Native upstream contract |
| ---: | --- | --- | --- |
| 52 | `flowith` | Chat Completions | `POST /ai/chat?mode=general` stream at `edge.flowith.net` |
| 55 | `langfast` | Chat Completions | Supabase `POST /functions/v1/initiate-prompt-run` plus Socket.IO EIO4 events |
| 56 | `liaobots` | Chat Completions | `POST /api/chat` SSE at `liaobots.work` |

The three adapters are registered in the gateway. It never starts Playwright or
another helper process, follows redirects, reads a browser profile, creates an
account, refreshes anonymous quota, harvests cookies, or forwards caller
authentication headers. Because these providers expose no conversation handle,
requests carrying `X-COT-Session` are rejected rather than presented as if
continuity were supported.

```go
client := webnext.New(source)
defer client.Close()
response, err := client.Do(ctx, "chat", model, stream, body, headers)
```

Only text Chat Completions are accepted. Unknown request fields, multimodal
content, tool calls, unsupported roles, empty messages, conflicting model or
stream selections, and non-user final turns are rejected. Events, cumulative
input, and converted output are bounded, and closing a response closes the
upstream HTTP or WebSocket connection. Each adapter follows its source's
completion condition: Flowith and LiaoBots complete at clean upstream EOF;
EaseMate is unsupported; LangFast requires `completion_finished` or a
completed execution chunk.

## Credentials

Credentials are read only from `Source.KeyEnv` at request time. A plain value
is accepted for Flowith as an optional Cookie. LangFast requires a JSON object
with its existing access token and the deployment's configured Supabase anon
key; `user_id` and `socket_url` are optional:

```json
{"access_token":"existing-jwt","user_id":"existing-sub","socket_url":"wss://...","supabase_anon_key":"configured-public-key"}
```

Flowith may additionally receive `supabase_url` and `supabase_anon_key` in its
credential object when the deployment requires the reference's
preauthorization request. Those values are explicit configuration; the
adapter does not use the reference's `.env` or embedded account state.

LiaoBots requires an object containing both an existing authorization code and
an existing Cookie. Both spellings `authCode`/`auth_code` are accepted:

```json
{"authCode":"existing-auth-code","cookie":"existing-cookie"}
```

The LiaoBots reference contains a `getFreshToken` call, quota-consuming signup
logic, and a fallback HAR cookie. Those paths are deliberately not ported.
The native client sends the supplied code as `X-Auth-Code` and the supplied
Cookie to `/api/chat`; an expired credential fails and must be replaced by the
operator.

## Source-specific behavior

EaseMate's pinned project obtains the request body by intercepting a browser
request and does not publish a standalone body schema. It remains a research
entry and is intentionally unsupported by the native adapter; no guessed
payload is sent to the provider.

Flowith sends the reference `stream`, stripped `flowith-` model, complete text
message history, and a fresh `nodeId`. The response adapter forwards each
non-empty upstream line verbatim, preserving spaces and any literal `data:`
prefix, and completes at clean upstream EOF. If Supabase fields are supplied,
it performs the reference's preauthorization GET before the chat request. The
Cloudflare-aware `cloudscraper` browser workaround is not ported, so a
deployment that requires that challenge can reject the direct request.

LangFast first opens a Socket.IO EIO4 WebSocket using the explicitly supplied
access token, then posts the reference `run_id`, the explicitly configured
`Source.Project` as `prompt_id`,
`prompt_meta`, `test_cases`, and `created_by` envelope to Supabase. The
`execution:chunk` event is cumulative; the client emits only the verified new
suffix and rejects a revised prefix. `completion_finished` or a completed
execution chunk is required. No signup or credential pool is implemented.

LiaoBots sends the reference model metadata, `conversationId`, complete text
messages, and prompt fields. It parses only JSON `data:` records containing the
source's `content` field, ignores other records like the pinned worker, and
completes at clean upstream EOF. Upstream error bodies and messages are not
copied into adapter errors.

## Evidence and limits

The wire facts were read from these pinned Apache-2.0 repositories. Their
licenses remain in the reference checkouts; no source file is copied into the
runtime package.

| Repository | Commit | Relevant source |
| --- | --- | --- |
| [lza6/easemate-2api](https://github.com/lza6/easemate-2api/tree/eb9713e7988243eb7a87076cab54c050bb4fbcf0) | `eb9713e7988243eb7a87076cab54c050bb4fbcf0` | `app/providers/easemate_provider.py` |
| [lza6/flowith-2api](https://github.com/lza6/flowith-2api/tree/41615dda805fd50769dab3d56fea37064a60dab2) | `41615dda805fd50769dab3d56fea37064a60dab2` | `app/providers/flowith_provider.py` |
| [lza6/langfast-2api](https://github.com/lza6/langfast-2api/tree/3f6d98609fe5d8532fc05b5da2ce6a13356d5c69) | `3f6d98609fe5d8532fc05b5da2ce6a13356d5c69` | `app/providers/langfast_provider.py`, `app/services/socketio_manager.py` |
| [lza6/liaobots-2api-cfwork](https://github.com/lza6/liaobots-2api-cfwork/tree/60f449528dda07b2788b09e33936bde836053e70) | `60f449528dda07b2788b09e33936bde836053e70` | `worker.js` |

Tests use local `httptest` fixtures and a local WebSocket server. They verify
request routing and payloads, explicit credential isolation, Socket.IO event
correlation, cumulative output handling, cancellation-safe closure, and
truncation rejection. They do not claim that any private web service is
currently available or that an account is eligible.
