# Enterprise and product web sources

internal/providers/enterpriseweb contains small native Go adapters for the
private protocols that have an executable request and response contract in the
pinned inventory. They are opt-in and require explicit credentials in
Source.KeyEnv. The HTTP adapters never start a browser, create an account,
refresh anonymous quota, import a local profile, or forward caller
authentication headers. Google AI Mode is the browser-backed CDP exception;
it uses a fresh configured tab without reading browser credentials. Requests
carrying X-COT-Session are rejected because these adapters create independent
upstream conversations.

The current status is:

| Inventory | Source | Native status |
| ---: | --- | --- |
| 84 | Google AI Mode | implemented with an explicitly configured Chrome CDP session; no HTTP credential path |
| 85 | MaxAI | implemented with the signed /gpt/cwc/chat SSE contract |
| 86 | Merlin | research-only; no pinned direct request contract |
| 88 | Monica | research-only; no pinned direct request contract |
| 89 | Notion AI Web | implemented with the cookie-authenticated runInferenceTranscript NDJSON contract |
| 90 | Opera Aria | implemented with the explicit refresh-token exchange and v1/v2 SSE contracts |
| 92 | Raycast | research-only; no pinned direct request contract |
| 93 | Sider | research-only; no pinned direct request contract |

The native adapters accept text Chat Completions only. Unknown fields,
multipart content, unsupported roles, empty messages, and a non-user final
turn return HTTP 422. Input bytes, each event, cumulative upstream bytes,
converted output bytes, and buffered output are bounded. Context cancellation
closes the response and upstream transport for streaming and non-streaming
requests. Upstream status bodies and messages are not copied into adapter
errors.

Google AI Mode requires `browser.enabled` and the configured loopback CDP
endpoint. It opens a fresh tab, performs the pinned Google search and AI Mode
DOM flow, and closes that tab when the request ends or its context is
cancelled. It does not read cookies or tokens from the browser.

## Credentials

MaxAI requires a JSON value containing an existing access token, device id,
user id, and the four chat signer values obtained from the deployment's own
public bundle (hmac_key, aes_key, context_key, and app_version; the
context key is the 40-hex payload slot). The native client signs each request
with the reference HMAC-SHA1, SM3, OpenSSL-compatible AES envelope and Firefox
identity headers. It does not fetch signer constants, refresh tokens, or
perform browser login.

    {"access_token":"existing-token","device_id":"existing-device","user_id":"existing-user","hmac_key":"deployment-value","aes_key":"deployment-value","context_key":"0123456789abcdef0123456789abcdef01234567","app_version":"webpage_1.2.3"}

Notion requires an existing token_v2 cookie, an explicit active user id,
and a workspace space_id either in the credential object or
Source.Project. A bare KeyEnv value is treated as a token_v2 value.
The request uses a fresh thread UUID and sends a full text transcript; no
thread id is returned for reuse.

    {"token_v2":"existing-token-v2","space_id":"existing-space-id","user_id":"existing-user-id"}

Opera Aria accepts an existing access token or refresh token. If only the
refresh token is supplied, it performs the documented grant_type=refresh_token
exchange with client_id=mini and scope=shodan:aria. The anonymous
client-credentials and anonymous-signup paths in the reference are not
implemented. The native adapter supports aria (v2) and aria-legacy (v1);
media upload is unsupported.

    {"refresh_token":"existing-refresh-token"}

## Source evidence

The wire behavior was read from the pinned reference checkouts; no reference
source is copied into the runtime package.

* [g4f GoogleAiMode.py](https://github.com/xtekky/gpt4free/blob/502b0e2e8d82292a39d2c3d9da07125322870583/g4f/Provider/search/GoogleAiMode.py) uses CDP navigation, a consent click, an AI Mode DOM click, and DOM text extraction. The Go adapter keeps that browser-only boundary.
* [g4f OperaAria.py](https://github.com/xtekky/gpt4free/blob/502b0e2e8d82292a39d2c3d9da07125322870583/g4f/Provider/OperaAria.py) defines the Opera OAuth, v1/v2 request payloads, identity headers, and SSE fields.
* [OmniRoute maxai protocol.ts](https://github.com/OmniRoute/OmniRoute/blob/ba597b631d22d85e56db6982f24b7d1ebe238df9/open-sse/executors/maxai/protocol.ts), [signing.ts](https://github.com/OmniRoute/OmniRoute/blob/ba597b631d22d85e56db6982f24b7d1ebe238df9/open-sse/executors/maxai/signing.ts), and [stream.ts](https://github.com/OmniRoute/OmniRoute/blob/ba597b631d22d85e56db6982f24b7d1ebe238df9/open-sse/executors/maxai/stream.ts) define the signed body, proof, AES envelope, and mergeable text SSE frames.
* [OmniRoute notion-web.ts](https://github.com/OmniRoute/OmniRoute/blob/ba597b631d22d85e56db6982f24b7d1ebe238df9/open-sse/executors/notion-web.ts), [notionTranscriptBuilder.ts](https://github.com/OmniRoute/OmniRoute/blob/ba597b631d22d85e56db6982f24b7d1ebe238df9/open-sse/services/notionTranscriptBuilder.ts), and [notionStreamParser.ts](https://github.com/OmniRoute/OmniRoute/blob/ba597b631d22d85e56db6982f24b7d1ebe238df9/open-sse/services/notionStreamParser.ts) define the authenticated transcript and NDJSON patch/record-map extraction. The reference uses browser TLS impersonation; this Go adapter does not claim that direct transport will pass Notion's edge.
* [notion-2api notion_provider.py](https://github.com/lza6/notion-2api/blob/2581198765ffc1f3acd4402e39f574898cee3b64/app/providers/notion_provider.py) corroborates the token_v2, space_id, user_id, and runInferenceTranscript boundary.

The native tests use local httptest fixtures. They prove payload shape,
credential isolation, signing primitive vectors, SSE/NDJSON extraction,
refresh-token routing, cancellation closure, bounds, and the HTTP 422
unsupported contract. They do not prove that private services currently accept
any account or that a credential is eligible.
