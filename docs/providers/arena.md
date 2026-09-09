# Arena

The `arena` adapter implements the private `POST /nextjs-api/stream/create-evaluation`
contract from [the pinned OmniRoute executor](https://github.com/diegosouzapw/OmniRoute/blob/ba597b631d22d85e56db6982f24b7d1ebe238df9/open-sse/executors/lmarena.ts)
and its `lmarena/stream.ts` parser. It sends one user text message in direct battle
mode with UUIDv7 evaluation/message identifiers. Configure an exact upstream model
UUID; this adapter does not resolve display names or fetch the model catalog.

Set `key_env` to an environment variable holding your complete cookie, or JSON
`{"cookie":"...","recaptchaV3Token":"..."}`. The optional CAPTCHA token must come
from your own browser session. The adapter does not solve challenges or obtain
anonymous sessions.

Responses are buffered until a valid participant A or unprefixed finish frame.
Participant B text is ignored. A step finish alone cannot complete the response;
truncated, malformed, unsupported and error frames fail without returning partial
success. `length` is preserved as the OpenAI finish reason. Tools, attachments,
history and system messages are rejected. Usage is not invented.

Local HTTP fixtures verify payloads, UUID version, authentication, participant
isolation, finish handling, early close and error sanitization. Real upstream
access has not been verified. Unlike the reference's wreq Chrome impersonation,
this implementation uses native Go TLS, so Cloudflare may reject it. This is an
experimental executable path, not a claim of successful account access.
