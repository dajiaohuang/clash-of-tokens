# China web providers: Emohaa, Spark, QwenCN, and Metaso

`internal/providers/chinaremaining` contains source-bound Go adapters for
private web endpoints. They are not official provider APIs. Credentials are
read only from each source's `Source.KeyEnv`; the package never reads a browser
profile or accepts credentials from the incoming request.

The adapters support one independent temporary turn per request. They accept
only `chat` requests with non-empty `system`, `user`, or `assistant` text
messages. Tool calls, multimodal content, unknown request fields, and
`X-COT-Session` continuation are rejected with `ErrUnsupported` (HTTP 422).
Streaming output is converted to OpenAI Chat Completions SSE and ends with a
terminal chunk followed by `[DONE]`; converted SSE bytes are bounded
cumulatively. Non-streaming output is buffered and is returned only after a
valid upstream terminal event. These private sources do not expose token
accounting, so converted responses omit `usage` rather than inventing counts.
Metaso also permits a clean EOF after non-empty answer text because its browser
transport can close without `[DONE]`; an empty answer, a transport read error,
or a Metaso/Qwen application error remains a failed request. Qwen cumulative
snapshots that revise already streamed text are rejected because SSE cannot
retract an earlier delta.

## Sources

| source | adapter | default endpoint | model IDs | contract |
| --- | --- | --- | --- | --- |
| Emohaa | `emohaa` | `https://ai-role.cn` | `emohaa` | temporary ID, XSS MD5 headers, text SSE, DELETE cleanup |
| Spark | `spark-web` | `https://xinghuo.xfyun.cn` | `spark` | temporary chat list, multipart request, base64 SSE, DELETE cleanup |
| Qwen mainland | `qwen-web-cn` | `https://api.tongyi.com` | configured model names such as `qwen-plus` | record-list prewarm and cumulative text SSE |
| Metaso | `metaso` | `https://metaso.cn` | `concise`, `detail`, `research`, optionally `-scholar` | meta token, temporary search session, JSON SSE events |

The default endpoint is used only when `Source.BaseURL` is empty. External
production endpoints must use HTTPS; HTTP is accepted only for loopback test
servers. Every upstream response and converted body is bounded.

## Credentials

Emohaa accepts a caller-owned bearer token in `KeyEnv`; a `Bearer ` prefix is
optional. Spark requires a JSON object with both the caller-owned SSO session
and the current webpage field:

```json
{"sso_session_id":"YOUR_SSO_SESSION_ID","gt_token":"YOUR_CURRENT_GT_TOKEN"}
```

The old source snapshot contained a 1908-byte webpage `GtToken`, but it is a
captured anti-bot grant rather than a public constant. The adapter therefore
does not embed or silently substitute it.

Qwen mainland requires a JSON credential with the complete browser cookie and
matching XSRF token. Multiple accounts may be configured and selected by model
index:

```json
{
  "accounts":[
    {"cookie":"YOUR_COOKIE", "xsrf_token":"YOUR_XSRF_TOKEN", "models":["qwen-plus"]}
  ],
  "model_accounts":{"qwen-plus":0}
}
```

Metaso requires a JSON credential containing the `uid-sid` session token:

```json
{"token":"YOUR_UID-YOUR_SID"}
```

JSON credentials reject unknown fields, line breaks, and oversized values.
They are never logged. Removing a value from the environment does not revoke
the upstream session; users must revoke it at the source.

## Metaso browser mode

When the supplied `config.Browser` has `Enabled` set, Metaso uses its configured
Chrome DevTools Protocol endpoint. It sets the supplied `uid` and `sid` cookies
in a fresh tab, performs a same-origin fetch, and reads the bounded stream
through a CDP page queue. Closing the response or cancelling its context closes
the tab and stops the fetch. When browser mode is disabled, the same documented
`/api/searchV2` SSE contract is requested directly; deployments that require
browser-only checks should enable CDP mode.

## Fixed references

The implementation follows these local, pinned source snapshots:

| source | local reference | use |
| --- | --- | --- |
| `emohaa-free-api`, head `809fe6e83ef3a853a08494fcbb77cf8cafbdf507` | `.clash-tokens/reference/china/emohaa-src-api-controllers-chat.ts`, `emohaa-src-api-routes-chat.ts` | XSS, temporary ID, prompt, and SSE shape |
| `spark-free-api`, head `ec2c4c4042fe096c9ffb98e2aa3d3d724eab488c` | `.clash-tokens/reference/china/spark-src-api-controllers-chat.ts`, `spark-src-api-routes-chat.ts` | SSO cookie, multipart fields, webpage `GtToken`, and base64 SSE |
| `lza6/Qwen-2api`, commit `4f793ac52a3e9e22fc9af17795aba492d5be2723` | `.clash-tokens/reference/china/Qwen-2api/app/providers/text_provider.py` | mainland endpoint, prewarm payload, cumulative text conversion |
| `metaso-free-api`, head `53e886172ee0563d50bf7f7277cc45e97d552b04` | `.clash-tokens/reference/china/metaso-src-api-controllers-chat.ts`, `metaso-src-api-routes-chat.ts` | session creation, meta token, search stream, model modes |

These are source-contract tests and local HTTP/CDP plumbing tests. No live
account, cookie, browser profile, or upstream availability is bundled.
