# Coding provider adapters

`internal/providers/coding` contains native Go clients for coding products
whose upstream wire protocols are different from the ordinary gateway
providers. The package does not start a third party proxy and does not read
product credential files.

The common contract is:

```go
client := coding.New(source)
defer client.Close()
response, err := client.Do(ctx, protocol, model, stream, body, headers)
```

`body` is the gateway request body for the selected protocol. The returned
`http.Response` keeps the upstream status for non-2xx responses. Successful
streaming responses expose a gateway-compatible SSE body; non-streaming
responses expose a JSON body. Request context cancellation is passed to the
upstream HTTP request.

## Adapter matrix

| Adapter | Gateway protocol | Upstream route | Authentication | Product fields |
| --- | --- | --- | --- | --- |
| `kiro` | `messages`, `chat` | `POST /generateAssistantResponse` | `Authorization: Bearer $key_env` | AWS Event Stream request/response; text and final data URL/base64 image prompts; messages and chat output formatters |
| `iflow` | `chat`, `messages` | `POST /chat/completions` | `Authorization: Bearer $key_env` | OpenAI-compatible request; messages requests and SSE responses are converted |
| `antigravity` | `gemini` | `POST /v1internal:generateContent` or `POST /v1internal:streamGenerateContent?alt=sse` | `Authorization: Bearer $key_env` | Cloud Code Assist envelope; `project` is required |

The adapter appends its route to `source.base_url`. Configure the host or API
prefix as the base URL, without query strings, fragments, user info, or a
pre-existing adapter route. HTTPS is required except for loopback endpoints
used in local tests.

## Configuration shape

Kiro and Antigravity are registered in the gateway and preset catalog. iFlow
remains outside the runtime registry because its reference announces shutdown.
Use the existing fields below for Kiro:

```json
{
  "id": "kiro-primary",
  "provider": "coding",
  "adapter": "kiro",
  "base_url": "https://codewhisperer.us-east-1.amazonaws.com",
  "key_env": "COT_KIRO_KEY",
  "enabled": true,
  "local": false,
  "paid": false,
  "max_inflight": 1,
  "quota_domain": "kiro-primary",
  "quota_max_inflight": 1,
  "models": [{
    "id": "kiro-model",
    "upstream": "kiro-model",
    "protocols": ["messages"],
    "tier": "unrated",
    "tools": "none",
    "vision": true,
    "max_input_bytes": 1048576
  }]
}
```

Use `https://apis.iflow.cn/v1` for the iFlow API base and
`https://cloudcode-pa.googleapis.com` for production Antigravity when those
services are available. Set `project` explicitly for Antigravity; the native
client does not discover or load a project from a local login. `key_env` is the
only credential source and must name an environment variable containing the
provider credential.

Caller headers are not forwarded. The client creates provider authentication
headers itself; the optional `X-COT-Session` header only selects the caller's
conversation/session key and is never treated as a credential.

The adapters fail closed on unknown request fields, unsupported message roles,
and unsupported message or content block fields. Kiro currently rejects
generation controls, system instructions, tool definitions, and tool
call/result blocks because this client does not yet preserve those semantics
through the CodeWhisperer envelope and response formatter. Kiro preserves
text and a final inline image; remote image URLs and images in historical turns
are rejected. These values are never silently flattened into text or replaced
with only the final user message.

## Evidence and limits

The implementation was cross-checked against pinned local source snapshots:

| Snapshot | Revision | License | Used for |
| --- | --- | --- | --- |
| `.clash-tokens/reference/coding/kiro2api` | `a2837e91d0f93d1f340ce43911c31b04c257c0d5` | MIT | Kiro endpoint, headers, CodeWhisperer request and AWS Event Stream framing |
| `.clash-tokens/reference/coding/CLIProxyAPI` | `7fac6b15bcfe5ea55c18c9eaec8e5b7e6457d974` | MIT | Antigravity Cloud Code Assist route and envelope conventions |
| `.clash-tokens/reference/coding/iflow-cli` | `4642808afbc6580ac117d930f6c64ac0d84955c7` | MIT | iFlow OpenAI-compatible base URL and request conventions |

These snapshots are implementation references, not a guarantee that a live
service remains available or that its private wire contract is stable. The
iFlow snapshot announces service shutdown on 17 April 2026 (UTC+8), so the
iFlow adapter should remain clearly marked unavailable until a live endpoint
and credential are independently validated. The repository's `httptest`
coverage verifies request shape, credential isolation, native stream parsing,
non-stream conversion, upstream errors, CRC/truncation behavior, and context
cancellation; it does not claim live provider coverage.
