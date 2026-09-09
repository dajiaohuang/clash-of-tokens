# Major web providers

`internal/providers/majorweb` contains independent Go clients for the
source-backed Claude Web, Grok Web, Grok Console, Grok Build, and Genspark
conversation protocols. The package has the same lifecycle shape as the
other native clients:

```go
c := majorweb.New(source)
defer c.Close()
resp, err := c.Do(ctx, protocol, model, stream, body, clientHeaders)
```

Credentials are read only from `Source.KeyEnv`. A plain value is treated as a
provider cookie/token; a bounded JSON object may contain `sessionKey`,
`access_token`, `token`, `cookie`, `org_id`, and `user_id`. `AccountIDEnv`, when
set, supplies the account or organization identifier. Caller headers are not
forwarded. Redirects are disabled, HTTPS is required away from loopback, and
request cancellation is passed to upstream HTTP/WebSocket operations.

| Adapter | Protocol | Upstream operation |
| --- | --- | --- |
| `claude-web` | `chat` | Resolve `GET /api/organizations`, create a new conversation with `POST /api/organizations/{org}/chat_conversations`, then stream `POST .../completion` SSE. The `default` model alias lets the product choose its default; explicit model IDs are forwarded. |
| `grok-web` | `chat` | Open a request-local `wss://.../ws/mgw/?uid=...` Gateway session, send `session.create`, `conversation.item.create`, and `response.create`, then convert Gateway events to Chat Completions SSE. |
| `grok-console` | `chat`, `responses` | Exchange the configured SSO cookie at `/v1/dpop/token` for a token bound to an ephemeral P-256 key, then send a signed DPoP request to `/v1/responses`. |
| `grok-build` | `chat`, `responses` | `POST {base}/responses`; the default base is `https://cli-chat-proxy.grok.com/v1`. |
| `genspark` / `genspark-web` | `chat` | `POST /api/copilot/ask` using the reference `COPILOT_MOA_CHAT` payload and convert SSE events. |

Each request is deliberately stateless at the adapter boundary. It accepts
exactly one non-empty user text turn and rejects history, assistant/system
roles, tools, attachments, multimodal content, and unknown request fields.
This prevents a generic gateway from silently dropping context or pretending
that a web product supports native tools. Grok Web requires a user ID because
the Gateway URL and cookie are account-bound; the adapter does not guess or
fetch one.

Chat streaming output is normalized to OpenAI Chat Completions SSE, including a
stable response ID, text deltas, reasoning deltas where the source exposes
them, a terminal finish chunk, and `[DONE]`. Non-streaming calls aggregate the
same converted stream before returning JSON. Native Responses calls preserve
the Responses format, with bounded stream validation. Truncated or malformed source
events are errors. Upstream non-2xx responses remain upstream responses so the
gateway can preserve their status and apply its error handling.

The package does not start browsers, solve CAPTCHA or Cloudflare challenges,
refresh accounts, rotate cookies, share sessions, enable hidden tools, or
claim that a live account is usable. The tests use local `httptest` fixtures
for HTTP contracts and parser behavior; no real generation request has been
made.

## Reference provenance

Only protocol facts were reimplemented. No source file was copied into the
provider package.

| Repository and pinned commit | Protocol paths read |
| --- | --- |
| [yushangxiao/claude2api](https://github.com/yushangxiao/claude2api/tree/c387010d9a102754a3d64a8cf9dc0ae82e958d1e), `c387010d9a102754a3d64a8cf9dc0ae82e958d1e` | `core/api.go`, `service/handle.go`, `model/openai.go` |
| [chenyme/grok2api](https://github.com/chenyme/grok2api/tree/44a390b890e7a3e0dd209b95b8c29a9f2b1be8dd), `44a390b890e7a3e0dd209b95b8c29a9f2b1be8dd` | `backend/internal/infra/provider/web/gateway.go`, `web/chat.go`, `web/headers.go`, `console/adapter.go`, `console/dpop.go`, `cli/adapter.go` |
| [deanxv/genspark2api](https://github.com/deanxv/genspark2api/tree/99b038b5f3fa9f64c6d1babc3e659ccb87be17ec), `99b038b5f3fa9f64c6d1babc3e659ccb87be17ec` | `controller/chat.go`, `model/openai.go`, `common/config/config.go` |
| [OmniRoute](https://github.com/diegosouzapw/OmniRoute/tree/ba597b631d22d85e56db6982f24b7d1ebe238df9), `ba597b631d22d85e56db6982f24b7d1ebe238df9` | `docs/reference/PROVIDER_REFERENCE.md` for provider categorization and protocol boundaries |

The pinned checkouts remain under `.clash-tokens/reference/majorweb/` for
auditability. They are not runtime dependencies and must never contain local
account credentials.
