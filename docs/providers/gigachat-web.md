# GigaChat Web

This adapter uses native Go Chrome DevTools Protocol (CDP) automation against
the signed-in GigaChat web application. Chrome owns login, cookies, and request
headers; the gateway does not extract or persist them.

The captured GigaChat `gigachat-neo/0.9.4` bundle sends a new text turn with
`POST /api/v0/sessions/request` (or a session-scoped `/request` path) and uses
`Accept: text/event-stream, application/json`. Its JSON SSE lifecycle is
`ACCEPTED`, zero or more `IN_PROGRESS` deltas, and `READY`; `ERROR` terminates
the turn. The adapter captures only those same-origin session request paths,
requires the correlated `ACCEPTED` and `READY` messages, and rejects a clean
EOF before `READY` as truncation.

The web input has a stable `#chat-input-textarea` native textarea. The submit
control is exposed by the stable `chat-input:submit` action id. The adapter
accepts one user text message, fills that textarea, activates the page's own
submit control, and returns the page's answer. OpenAI-compatible streaming is
pseudo-streamed after the complete `READY` message is captured. History,
tools, attachments, arbitrary model labels, and `X-COT-Session` continuation
are rejected rather than silently discarded; the sole model selector is
`web`.

The public portal is `https://giga.chat/portal` and documents login by phone or
Sber ID. The app route was found in a public educational conference PDF's
GigaChat link (`https://conf.nis.edu.kz/wp-content/uploads/2024/08/russkij-yazyk-avgustovskaya-konferencziya-czop-2024.pdf`), not in a user's
session or chat history. Its HTML has canonical URL `https://giga.chat` and
renders the generic application shell; it is an old public app mount, rather
than an account or session identifier. In the captured environment
`/gigachat` redirected through an anti-bot path, while the public app route
`/055bfe55-33da-41ee-bf6e-26d57c3649d1/home` returned the shell. The catalog
default uses `https://giga.chat` and the adapter navigates that route; an
explicit base URL path is honored for controlled deployments and fixtures.

The pinned 0.9.4 browser assets were fetched from the page's public CDN URLs:
`https://cdn-app.giga.chat/gigachat-neo/0.9.4/assets/main.01eaf033a7bfc47c.js`
(SHA-256 `05677a80a79cd4b5c280aa678f80666459f27d3649fbf3f0b0ebb8f6a5dd93dc`),
`1909.5723b5fb7f9592ab.js` (SHA-256
`aa9e7c6bd2167cdd0c41b0eb0e8c7b2ed7d1c226effda560b27664faa9f21661`), and
`1051.0bb08fedc4cf4692.js` (SHA-256
`7cc85eecf85d806b8e6d25b924676b382f45265a648b078d657f91091f830036`). The
`1051` asset contains the textarea ID and submit action; `1909` contains the
session request paths and `ACCEPTED`/`IN_PROGRESS`/`READY`/`ERROR` lifecycle.

The g4f reference at commit `502b0e2e8d82292a39d2c3d9da07125322870583`
(`g4f/Provider/needs_auth/GigaChat.py`) is an API adapter. It writes a bundled
Russian trust-root certificate, exchanges an explicit `api_key` at
`https://ngw.devices.sberbank.ru:9443/api/v2/oauth` with a generated `RqUID`
and the `GIGACHAT_API_PERS` scope, then calls
`https://gigachat.devices.sberbank.ru/api/v1/chat/completions` with a bearer
access token. Its `url` points to the developer site, not to an executable
web-chat session. It requires the caller to supply the API credential.

The pinned OmniRoute reference at commit
`ba597b631d22d85e56db6982f24b7d1ebe238df9` has only an API registry entry at
`open-sse/config/providers/registry/gigachat/index.ts`: the base URL is the
Sber API, `executor` is `default`, and the auth type is `apikey` with a bearer
header. Its `services/gigachatAuth.ts` likewise implements the OAuth token
exchange for supplied credentials. Those API references remain separate from
the browser transport above.

The local CDP fixture validates page navigation, textarea submission, bounded
capture, SSE lifecycle parsing, and buffered/stream response conversion.
`live_verified` remains false until a real signed-in GigaChat account is
exercised.
